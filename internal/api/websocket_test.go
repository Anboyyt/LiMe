package api

import (
	"bufio"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebSocketHandshakeAndPing(t *testing.T) {
	srv := httptest.NewServer(newTestHandler(t))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("GET /socket HTTP/1.1\r\n" +
		"Host: test\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"))

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("status = %q", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}

	// 发一个带掩码的 ping，期望收到 pong
	payload := []byte("hi")
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x89, 0x80 | byte(len(payload)), mask[0], mask[1], mask[2], mask[3]}
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}

	opcode, got, err := readWSFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if opcode != 0xA || string(got) != "hi" {
		t.Fatalf("opcode=%x payload=%q, want pong hi", opcode, got)
	}
}
