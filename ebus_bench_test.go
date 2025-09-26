package ebus

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
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

// JSON decoder overhead (DisallowUnknownFields on)
func BenchmarkDecode_JSON(b *testing.B) {
	type decPayload struct {
		Foo string `json:"foo"`
		Bar int    `json:"bar"`
	}
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
