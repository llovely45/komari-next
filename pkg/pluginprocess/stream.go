package pluginprocess

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const maxFrameBytes = 8 << 20

// Stream is the newline-delimited JSON transport used by Go/WASI plugins.
// The handshake is the first line; all subsequent lines are Message values.
// Payload is encoded by encoding/json as base64 because it is a byte slice.
type Stream struct {
	scanner *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex
}

func NewStream(reader io.Reader, writer io.Writer) *Stream {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxFrameBytes)
	return &Stream{scanner: scanner, writer: writer}
}

func (s *Stream) ReadHandshake() (Handshake, error) {
	var handshake Handshake
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return handshake, fmt.Errorf("read plugin handshake: %w", err)
		}
		return handshake, io.EOF
	}
	if err := json.Unmarshal(s.scanner.Bytes(), &handshake); err != nil {
		return handshake, fmt.Errorf("decode plugin handshake: %w", err)
	}
	return handshake, nil
}

func (s *Stream) WriteHandshake(handshake Handshake) error {
	return s.writeJSON(handshake)
}

func (s *Stream) ReadHandshakeResponse() (HandshakeResponse, error) {
	var response HandshakeResponse
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return response, fmt.Errorf("read plugin handshake response: %w", err)
		}
		return response, io.EOF
	}
	if err := json.Unmarshal(s.scanner.Bytes(), &response); err != nil {
		return response, fmt.Errorf("decode plugin handshake response: %w", err)
	}
	return response, nil
}

func (s *Stream) WriteHandshakeResponse(response HandshakeResponse) error {
	return s.writeJSON(response)
}

func (s *Stream) ReadMessage() (Message, error) {
	var message Message
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return message, fmt.Errorf("read plugin message: %w", err)
		}
		return message, io.EOF
	}
	if err := json.Unmarshal(s.scanner.Bytes(), &message); err != nil {
		return message, fmt.Errorf("decode plugin message: %w", err)
	}
	if err := validateMessage(message); err != nil {
		return Message{}, err
	}
	return message, nil
}

func (s *Stream) WriteMessage(message Message) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	return s.writeJSON(message)
}

func (s *Stream) writeJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode plugin protocol frame: %w", err)
	}
	if len(data) > maxFrameBytes {
		return fmt.Errorf("plugin protocol frame exceeds %d bytes", maxFrameBytes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write plugin protocol frame: %w", err)
	}
	return nil
}

func validateMessage(message Message) error {
	if message.Type != MessageCall && message.Type != MessageEvent && message.Type != MessageResult {
		return errors.New("plugin protocol message has an invalid type")
	}
	if message.Type == MessageCall && (message.RequestID == "" || message.Method == "") {
		return errors.New("plugin call requires request_id and method")
	}
	if message.Type == MessageResult && message.RequestID == "" {
		return errors.New("plugin result requires request_id")
	}
	if message.Type == MessageEvent && message.Method == "" {
		return errors.New("plugin event requires method")
	}
	return nil
}
