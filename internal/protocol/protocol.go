package protocol

import (
	"encoding/binary"
	"io"
)

type Type byte

const (
	TypeData   Type = 0x01
	TypeResize Type = 0x02
	TypeSignal Type = 0x03
	TypeKick   Type = 0x04
	TypeMode   Type = 0x05
	TypeEnv    Type = 0x06
)

const (
	// MaxPayloadSize is the maximum allowed size for a single packet payload (64KB).
	MaxPayloadSize = 64 * 1024
)

// WritePacket writes a typed packet with a payload to the writer.
func WritePacket(w io.Writer, t Type, payload []byte) error {
	if len(payload) > MaxPayloadSize {
		return io.ErrShortBuffer
	}
	// Header: Type (1) + Length (4)
	header := make([]byte, 5)
	header[0] = byte(t)
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadPacket reads a packet from the reader.
func ReadPacket(r io.Reader) (Type, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	
	t := Type(header[0])
	length := binary.BigEndian.Uint32(header[1:])
	
	if length > MaxPayloadSize {
		return 0, nil, io.ErrUnexpectedEOF
	}

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	
	return t, payload, nil
}

// ResizePayload encodes rows and cols into a byte slice.
func ResizePayload(rows, cols uint16) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:], rows)
	binary.BigEndian.PutUint16(buf[2:], cols)
	return buf
}

// DecodeResizePayload decodes the payload into rows and cols.
func DecodeResizePayload(data []byte) (uint16, uint16) {
	if len(data) < 4 {
		return 0, 0
	}
	rows := binary.BigEndian.Uint16(data[0:])
	cols := binary.BigEndian.Uint16(data[2:])
	return rows, cols
}
