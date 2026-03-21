package app

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

type asrServerFrame struct {
	MessageType byte
	Flags       byte
	Sequence    int32
	Payload     []byte
	ErrorCode   uint32
	ErrorMsg    string
}

func buildASRClientFrame(msgType byte, flags byte, serialization byte, compression byte, payload []byte) ([]byte, error) {
	header := []byte{
		(asrProtocolVersion << 4) | asrHeaderSizeWords,
		(msgType << 4) | (flags & 0x0F),
		(serialization << 4) | (compression & 0x0F),
		0x00,
	}
	compressed := payload
	if compression == asrCompressionGzip {
		var err error
		compressed, err = gzipCompress(payload)
		if err != nil {
			return nil, err
		}
	}
	out := make([]byte, 0, 8+len(compressed))
	out = append(out, header...)
	sizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBytes, uint32(len(compressed)))
	out = append(out, sizeBytes...)
	out = append(out, compressed...)
	return out, nil
}

func parseASRServerFrame(raw []byte) (*asrServerFrame, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("asr frame too short: %d", len(raw))
	}
	headerWords := raw[0] & 0x0F
	headerSize := int(headerWords) * 4
	if headerSize < 4 || len(raw) < headerSize {
		return nil, fmt.Errorf("invalid header size: %d", headerSize)
	}
	msgType := (raw[1] >> 4) & 0x0F
	flags := raw[1] & 0x0F
	compression := raw[2] & 0x0F

	idx := headerSize
	frame := &asrServerFrame{MessageType: msgType, Flags: flags}

	if msgType == asrMsgTypeError {
		if len(raw) < idx+8 {
			return nil, fmt.Errorf("invalid asr error frame")
		}
		frame.ErrorCode = binary.BigEndian.Uint32(raw[idx : idx+4])
		msgSize := int(binary.BigEndian.Uint32(raw[idx+4 : idx+8]))
		idx += 8
		if len(raw) < idx+msgSize {
			return nil, fmt.Errorf("invalid asr error payload size")
		}
		frame.ErrorMsg = string(raw[idx : idx+msgSize])
		return frame, nil
	}

	if flags == asrFlagPosSequence || flags == asrFlagLastNegSeq {
		if len(raw) < idx+4 {
			return nil, fmt.Errorf("invalid asr frame sequence")
		}
		frame.Sequence = int32(binary.BigEndian.Uint32(raw[idx : idx+4]))
		idx += 4
	}
	if len(raw) < idx+4 {
		return nil, fmt.Errorf("invalid asr frame payload size")
	}
	payloadSize := int(binary.BigEndian.Uint32(raw[idx : idx+4]))
	idx += 4
	if payloadSize < 0 || len(raw) < idx+payloadSize {
		return nil, fmt.Errorf("invalid asr frame payload length")
	}
	payload := raw[idx : idx+payloadSize]
	if compression == asrCompressionGzip {
		unzipped, err := gzipDecompress(payload)
		if err != nil {
			return nil, fmt.Errorf("gzip decode asr payload: %w", err)
		}
		payload = unzipped
	}
	frame.Payload = payload
	return frame, nil
}

func gzipCompress(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(payload []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
