package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "cache node URL")
	command := flag.String("command", "get", "set, get, or delete")
	key := flag.String("key", "", "cache key")
	value := flag.String("value", "", "value for set")
	ttlMS := flag.Int64("ttl-ms", 0, "TTL in milliseconds for set; zero means no expiry")
	timeout := flag.Duration("timeout", 15*time.Second, "HTTP request timeout")
	flag.Parse()

	if *key == "" {
		fail("-key is required")
	}

	client := &http.Client{Timeout: *timeout}
	var resp *http.Response
	var err error
	switch *command {
	case "set":
		body, marshalErr := json.Marshal(map[string]any{"key": *key, "value": *value, "ttl_ms": *ttlMS})
		if marshalErr != nil {
			fail(marshalErr.Error())
		}
		resp, err = client.Post(*baseURL+"/set", "application/json", bytes.NewReader(body))
	case "get":
		resp, err = client.Get(*baseURL + "/get?key=" + url.QueryEscape(*key))
	case "delete":
		req, requestErr := http.NewRequest(http.MethodDelete, *baseURL+"/delete?key="+url.QueryEscape(*key), nil)
		if requestErr != nil {
			fail(requestErr.Error())
		}
		resp, err = client.Do(req)
	default:
		fail("-command must be set, get, or delete")
	}
	if err != nil {
		fail(err.Error())
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		fail(readErr.Error())
	}
	fmt.Printf("HTTP %s\n%s", strconv.Itoa(resp.StatusCode), responseBody)
	if resp.StatusCode >= http.StatusBadRequest {
		os.Exit(1)
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
