package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type duplex struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func (d *duplex) Read(p []byte) (int, error)  { return d.in.Read(p) }
func (d *duplex) Write(p []byte) (int, error) { return d.out.Write(p) }

func frames(t *testing.T, bodies ...string) *duplex {
	t.Helper()
	var buffer bytes.Buffer
	for _, body := range bodies {
		if err := WriteFrame(&buffer, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return &duplex{in: bytes.NewReader(buffer.Bytes())}
}

func decodeAll(t *testing.T, raw []byte) []Response {
	t.Helper()
	var result []Response
	reader := bytes.NewReader(raw)
	for {
		frame, err := ReadFrame(reader, MaxFrame)
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		var response Response
		if err := json.Unmarshal(frame, &response); err != nil {
			t.Fatal(err)
		}
		result = append(result, response)
	}
}

// The shared fixture is also consumed by the Swift tests; both sides must agree.
func TestSharedContractCases(t *testing.T) {
	data, err := os.ReadFile("../../../../shared/contracts/ipc-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		ProtocolVersion int `json:"protocol_version"`
		Cases           []struct {
			Name    string `json:"name"`
			Padding int    `json:"request_padding_bytes"`
			Request string `json:"request"`
			Expect  struct {
				OK        bool   `json:"ok"`
				RequestID string `json:"request_id"`
				ErrorCode string `json:"error_code"`
				Gen       uint64 `json:"generation"`
			} `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.ProtocolVersion != ProtocolVersion || len(contract.Cases) < 8 {
		t.Fatalf("fixture out of sync: version %d, %d cases", contract.ProtocolVersion, len(contract.Cases))
	}
	for _, c := range contract.Cases {
		t.Run(c.Name, func(t *testing.T) {
			request := strings.ReplaceAll(c.Request, "%PAD%", strings.Repeat("a", c.Padding))
			conn := frames(t, request)
			if err := Serve(conn, BuildInfo{CoreVersion: "t", MihomoRevision: "r"}); err != nil {
				t.Fatal(err)
			}
			got := decodeAll(t, conn.out.Bytes())
			if len(got) != 1 {
				t.Fatalf("want one response, got %d", len(got))
			}
			r := got[0]
			if r.OK != c.Expect.OK || r.RequestID != c.Expect.RequestID || r.Generation != c.Expect.Gen {
				t.Fatalf("unexpected response %+v", r)
			}
			if c.Expect.ErrorCode != "" && (r.Error == nil || r.Error.Code != c.Expect.ErrorCode) {
				t.Fatalf("want error %s, got %+v", c.Expect.ErrorCode, r.Error)
			}
		})
	}
}

func TestHelloCarriesNoSecretsAndBuildIdentity(t *testing.T) {
	conn := frames(t, `{"request_id":"a","protocol_version":1,"generation":0,"command":"hello"}`)
	if err := Serve(conn, BuildInfo{CoreVersion: "9.9.9", MihomoRevision: "abc"}); err != nil {
		t.Fatal(err)
	}
	var hello HelloResult
	if err := json.Unmarshal(decodeAll(t, conn.out.Bytes())[0].Result, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.CoreVersion != "9.9.9" || hello.MihomoRevision != "abc" || hello.ProtocolVersion != 1 {
		t.Fatalf("%+v", hello)
	}
}

func TestShutdownStopsServingAfterReply(t *testing.T) {
	conn := frames(t,
		`{"request_id":"s","protocol_version":1,"generation":0,"command":"shutdown"}`,
		`{"request_id":"later","protocol_version":1,"generation":0,"command":"hello"}`)
	if err := Serve(conn, BuildInfo{}); err != nil {
		t.Fatal(err)
	}
	got := decodeAll(t, conn.out.Bytes())
	if len(got) != 1 || got[0].RequestID != "s" || !got[0].OK {
		t.Fatalf("%+v", got)
	}
}

func TestFramingFailuresTerminateSession(t *testing.T) {
	huge := make([]byte, 4)
	binary.BigEndian.PutUint32(huge, MaxFrame+1)
	zero := make([]byte, 4)
	truncated := append([]byte{0, 0, 0, 10}, []byte("abc")...)
	for name, raw := range map[string][]byte{"oversize": huge, "empty": zero, "truncated body": truncated, "truncated header": {0, 0}} {
		t.Run(name, func(t *testing.T) {
			conn := &duplex{in: bytes.NewReader(raw)}
			if err := Serve(conn, BuildInfo{}); err == nil {
				t.Fatal("expected framing error")
			}
			if conn.out.Len() != 0 {
				t.Fatal("no reply may be written on a broken stream")
			}
		})
	}
}

func TestCleanEOFIsNotAnError(t *testing.T) {
	if err := Serve(&duplex{in: bytes.NewReader(nil)}, BuildInfo{}); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedBodyIsNotAllocatedBeforeLimitCheck(t *testing.T) {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, 1<<31)
	if _, err := ReadFrame(bytes.NewReader(header), MaxFrame); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v", err)
	}
}
