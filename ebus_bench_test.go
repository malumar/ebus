package ebus

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- Benchmark Env / Tx / Payloads (stubs) ----------

type benchEnv struct{} // no Transactional here (used for NoTx benchmarks)

// Tx-enabled env for Tx benchmarks
type benchTxEnv struct{}

// Simple Tx that also stages RAW (to memory only)
type benchTx struct {
	staged int
	fail   bool
}

func (e benchTxEnv) BeginTx(readonly bool) (Tx, error) {
	return &benchTx{}, nil
}
func (t *benchTx) Commit() error {
	if t.fail {
		return errors.New("commit failed")
	}
	return nil
}
func (t *benchTx) Rollback() error { return nil }
func (t *benchTx) PutRaw(_ *Raw[ID8Byte]) error {
	t.staged++
	return nil
}

// Minimal payload (no RAW)
type benchPayload struct{ nop int }

func (p *benchPayload) Validate(_ benchEnv) error { return nil }
func (p *benchPayload) Handle(_ benchEnv) error   { p.nop++; return nil }
func (p *benchPayload) Commit(_ benchEnv)         {}
func (p *benchPayload) Rollback(_ benchEnv)       {}
func (p *benchPayload) PayloadType() PayloadType  { return 1 }

// Minimal payload for Tx env (no RAW)
type benchPayloadTx struct{ nop int }

func (p *benchPayloadTx) Validate(_ benchTxEnv) error { return nil }
func (p *benchPayloadTx) Handle(_ benchTxEnv) error   { p.nop++; return nil }
func (p *benchPayloadTx) Commit(_ benchTxEnv)         {}
func (p *benchPayloadTx) Rollback(_ benchTxEnv)       {}
func (p *benchPayloadTx) PayloadType() PayloadType    { return 2 }

// RAW payload implementing RawKeeper (for staging path)
type benchRawPayload struct {
	RawPayload[ID8Byte]
	nop int
}

func (p *benchRawPayload) Validate(_ benchTxEnv) error { return nil }
func (p *benchRawPayload) Handle(_ benchTxEnv) error   { p.nop++; return nil }
func (p *benchRawPayload) Commit(_ benchTxEnv)         {}
func (p *benchRawPayload) Rollback(_ benchTxEnv)       {}
func (p *benchRawPayload) PayloadType() PayloadType    { return 3 }

// JSON decode payload for decoder benchmark (implements Payload[any])
type jsonDecPayload struct {
	Foo string `json:"foo"`
	Bar int    `json:"bar"`
}

func (p *jsonDecPayload) Validate(_ any) error     { return nil }
func (p *jsonDecPayload) Handle(_ any) error       { return nil }
func (p *jsonDecPayload) Commit(_ any)             {}
func (p *jsonDecPayload) Rollback(_ any)           {}
func (p *jsonDecPayload) PayloadType() PayloadType { return 99 }

func fmtName(ts int64, pt int) string {
	return fmt.Sprintf("%d_%d_%d.raw.pending", ts, pt, time.Now().UnixNano())
}

// ---------- I/O Tx (WAL fsync) for I/O benchmarks ----------
type ioEnv struct{ dir string }
type ioTx struct {
	dir   string
	files []string
}

func (e ioEnv) BeginTx(readonly bool) (Tx, error) { return &ioTx{dir: e.dir}, nil }
func (t *ioTx) PutRaw(r *Raw[ID8Byte]) error {
	if err := os.MkdirAll(t.dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	name := filepath.Join(t.dir, "bench_"+strings.TrimSuffix(string(r.Meta.CorrelationID[:]), "")+".raw.pending")
	// Fallback: use event timestamp + type for uniqueness if correlation is zero
	if r.Meta.TimestampUnix != 0 {
		name = filepath.Join(t.dir, fmtName(int64(r.Meta.TimestampUnix), int(r.Type)))
	}
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(r.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // fsync file
		_ = f.Close()
		return err
	}
	_ = f.Close()
	t.files = append(t.files, name)
	return nil
}
func (t *ioTx) Commit() error {
	for _, tmp := range t.files {
		final := strings.TrimSuffix(tmp, ".pending")
		if err := os.Rename(tmp, final); err != nil {
			return err
		}
	}
	// best-effort dir fsync
	if d, err := os.Open(t.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	t.files = nil
	return nil
}
func (t *ioTx) Rollback() error {
	for _, tmp := range t.files {
		_ = os.Remove(tmp)
	}
	t.files = nil
	return nil
}

// RAW payload for ioEnv
type ioRawPayload struct {
	RawPayload[ID8Byte]
	cnt int
}

func (p *ioRawPayload) Validate(_ ioEnv) error   { return nil }
func (p *ioRawPayload) Handle(_ ioEnv) error     { p.cnt++; return nil }
func (p *ioRawPayload) Commit(_ ioEnv)           {}
func (p *ioRawPayload) Rollback(_ ioEnv)         {}
func (p *ioRawPayload) PayloadType() PayloadType { return 7 }

// ---------- Helpers ----------

func makePayloadsNoTx(n int) []Payload[benchEnv] {
	ps := make([]Payload[benchEnv], n)
	for i := 0; i < n; i++ {
		ps[i] = &benchPayload{}
	}
	return ps
}

func makePayloadsTx(n int) []Payload[benchTxEnv] {
	ps := make([]Payload[benchTxEnv], n)
	for i := 0; i < n; i++ {
		ps[i] = &benchPayloadTx{}
	}
	return ps
}

func makeRawPayloadsTx(n int) []Payload[benchTxEnv] {
	ps := make([]Payload[benchTxEnv], n)
	for i := 0; i < n; i++ {
		ps[i] = &benchRawPayload{}
	}
	return ps
}

// ---------- Benchmarks ----------

// Baseline: no Tx, no RAW, k payloads per event
func BenchmarkPublish_NoTx_NoRaw(b *testing.B) {
	bus := NewDefault(NewID8ByteHandler[benchEnv](0))
	env := benchEnv{}
	cases := []int{1, 4, 16, 64}

	for _, k := range cases {
		b.Run("payloads="+itoa(k), func(b *testing.B) {
			ps := makePayloadsNoTx(k)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bus.PublishAll(env, ps); err != nil {
					b.Fatalf("err: %v", err)
				}
			}
		})
	}
}

// Tx path: Tx + Stager (RAW staging), but payloads are no-RAW (PutRaw not called)
func BenchmarkPublish_Tx_NoRaw(b *testing.B) {
	bus := NewWithTx(NewID8ByteHandler[benchTxEnv](0))
	env := benchTxEnv{}
	cases := []int{1, 4, 16, 64}

	for _, k := range cases {
		b.Run("payloads="+itoa(k), func(b *testing.B) {
			ps := makePayloadsTx(k)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bus.PublishAll(env, ps); err != nil {
					b.Fatalf("err: %v", err)
				}
			}
		})
	}
}

// Tx path: Tx + Stager + RAW payloads (PutRaw called for each payload)
func BenchmarkPublish_Tx_WithRAW(b *testing.B) {
	bus := NewWithTx(NewID8ByteHandler[benchTxEnv](0))
	env := benchTxEnv{}
	cases := []int{1, 4, 16, 64}

	for _, k := range cases {
		b.Run("payloads="+itoa(k), func(b *testing.B) {
			ps := makeRawPayloadsTx(k)
			// Attach RAW envelopes (simulate PublishRaws decoding)
			rnd := make([]byte, 128)
			_, _ = rand.Read(rnd)
			for _, p := range ps {
				if rk, ok := p.(RawKeeper[ID8Byte]); ok {
					rk.SetRaw(&Raw[ID8Byte]{Type: 3, Body: rnd})
				}
			}

			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bus.PublishAll(env, ps); err != nil {
					b.Fatalf("err: %v", err)
				}
			}
		})
	}
}

// Parallel: many goroutines publishing small events (no Tx)
func BenchmarkPublish_NoTx_Parallel(b *testing.B) {
	bus := NewDefault(NewID8ByteHandler[benchEnv](0))
	env := benchEnv{}
	ps := makePayloadsNoTx(2)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := bus.PublishAll(env, ps); err != nil {
				b.Fatalf("err: %v", err)
			}
		}
	})
}

// Tx path with I/O WAL (fsync) — realistic staging cost
func BenchmarkPublish_Tx_WithRAW_IO(b *testing.B) {
	dir, err := os.MkdirTemp("", "ebus_bench_io_*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	bus := NewWithTx(NewID8ByteHandler[ioEnv](0))
	env := ioEnv{dir: dir}
	cases := []int{1, 4}
	for _, k := range cases {
		b.Run("payloads="+itoa(k), func(b *testing.B) {
			ps := make([]Payload[ioEnv], k)
			rnd := make([]byte, 256)
			_, _ = rand.Read(rnd)
			for i := 0; i < k; i++ {
				p := &ioRawPayload{}
				p.SetRaw(&Raw[ID8Byte]{Type: 7, Body: rnd})
				ps[i] = p
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bus.PublishAll(env, ps); err != nil {
					b.Fatalf("err: %v", err)
				}
			}
		})
	}
}

// Middleware overhead: Logging + Retry (successful path)
func BenchmarkMiddleware_Logging_Retry_NoTx(b *testing.B) {
	noop := func(string, ...any) {}
	bus := NewDefault(NewID8ByteHandler[benchEnv](0),
		LoggingByID[benchEnv, ID8Byte](noop),
		Retry[benchEnv, ID8Byte](1, time.Nanosecond),
	)
	env := benchEnv{}
	ps := makePayloadsNoTx(2)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := bus.PublishAll(env, ps); err != nil {
			b.Fatalf("err: %v", err)
		}
	}
}

// End-to-end PublishRaws: Decode -> PublishAll with RawKeeper
func BenchmarkPublishRaws_EndToEnd_Tx(b *testing.B) {
	bus := NewWithTx(NewID8ByteHandler[benchTxEnv](0))
	env := benchTxEnv{}
	dec := NewJSONDecoder[benchTxEnv]()
	dec.MustRegister(3, func() Payload[benchTxEnv] { return &benchRawPayload{} })
	cases := []int{1, 4, 16}
	body := []byte(`{}`)
	for _, k := range cases {
		b.Run("raws="+itoa(k), func(b *testing.B) {
			raws := make([]Raw[ID8Byte], k)
			for i := 0; i < k; i++ {
				raws[i] = Raw[ID8Byte]{Type: 3, Body: body}
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := bus.PublishRaws(env, dec, raws...); err != nil {
					b.Fatalf("err: %v", err)
				}
			}
		})
	}
}

// JSON decoder overhead (DisallowUnknownFields on)
func BenchmarkDecode_JSON(b *testing.B) {
	dec := NewJSONDecoder[any]()
	dec.MustRegister(99, func() Payload[any] { return &jsonDecPayload{} })

	body := map[string]any{"foo": "abc", "bar": 123}
	bts, _ := json.Marshal(body)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := dec.Decode(99, bts); err != nil {
			b.Fatalf("decode: %v", err)
		}
	}
}

// ---------- tiny utils ----------

func itoa(i int) string { return string(intToASCII(i)) }

func intToASCII(i int) []byte {
	var buf [20]byte
	pos := len(buf)
	if i == 0 {
		return []byte{'0'}
	}
	n := i
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return buf[pos:]
}
