package main

import (
	"bufio"
	"encoding/json"
	"fmt"
)

// Old recorded fixtures predate host initialization. Answer the SDK handshake
// transparently unless the fixture explicitly owns it; controls still get logged.
func readInput(stdin *bufio.Reader, expected string) ([]byte, error) {
	for {
		got, err := stdin.ReadBytes('\n')
		logStdin(got)
		if err != nil {
			return got, err
		}
		checkInput(got)
		subtype := subtypeOf(got)
		if subtype == "initialize" && expected != "initialize" {
			acknowledge(got)
			continue
		}
		if subtype == "interrupt" || subtype == "set_model" {
			acknowledge(got)
		}
		return got, nil
	}
}

func acknowledge(raw []byte) {
	var request struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &request)
	data, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": request.RequestID, "response": map[string]any{"still_queued": []string{}}}})
	fmt.Printf("%s\n", data)
}
