package protocol

import (
	"bytes"
	"testing"
)

func TestPacketSerialization(t *testing.T) {
	buf := new(bytes.Buffer)
	payload := []byte("hello world")
	
	if err := WritePacket(buf, TypeData, payload); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	
	typ, data, err := ReadPacket(buf)
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}
	
	if typ != TypeData {
		t.Errorf("Type mismatch. Got %d, want %d", typ, TypeData)
	}
	
	if string(data) != string(payload) {
		t.Errorf("Payload mismatch. Got %s, want %s", string(data), string(payload))
	}
}

func TestResizePayload(t *testing.T) {
	rows := uint16(24)
	cols := uint16(80)
	
	data := ResizePayload(rows, cols)
	r, c := DecodeResizePayload(data)
	
	if r != rows || c != cols {
		t.Errorf("Resize decode failed. Got %d,%d, want %d,%d", r, c, rows, cols)
	}
}

func FuzzReadPacket(f *testing.F) {
	// Add some valid seeds
	f.Add([]byte{0x01, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'})
	f.Add([]byte{0x02, 0, 0, 0, 4, 0, 24, 0, 80})
	
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		_, _, _ = ReadPacket(r)
	})
}

func FuzzDecodeResizePayload(f *testing.F) {
	f.Add([]byte{0, 24, 0, 80})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeResizePayload(data)
	})
}
