package target

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Control serves a target's counters over HTTP so the target can live in
// another network namespace from the runner.
//
// The alternative - keeping the target inside the load generator - forces the
// measured server and load generator into the same namespace, and a target in
// the wrong namespace turns "server to target" into a trip across the shaped
// link, which changes the number being measured.
type Control struct {
	server   *Server
	listener net.Listener
	http     *http.Server
}

// ServeControl exposes server on addr. Endpoints:
//
//	GET  /stats  -> Stats
//	POST /reset  -> zero the counters
//	GET  /health -> "ok"
func ServeControl(addr string, server *Server) (*Control, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	control := &Control{server: server, listener: listener}
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(server.Snapshot())
	})
	mux.HandleFunc("/reset", func(writer http.ResponseWriter, request *http.Request) {
		server.Reset()
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	})
	control.http = &http.Server{Handler: mux}
	go func() { _ = control.http.Serve(listener) }()
	return control, nil
}

// Close stops the control endpoint.
func (c *Control) Close() error {
	if c.http != nil {
		_ = c.http.Close()
	}
	return nil
}

// Remote is a target running in another process.
type Remote struct {
	baseURL string
	client  *http.Client
}

// DialRemote waits until the control endpoint answers and returns a handle.
func DialRemote(addr string, timeout time.Duration) (*Remote, error) {
	remote := &Remote{
		baseURL: "http://" + addr,
		client:  &http.Client{Timeout: timeout},
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := remote.client.Get(remote.baseURL + "/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return remote, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("target control endpoint %s did not become ready", addr)
}

// Snapshot reads the remote counters.
func (r *Remote) Snapshot() (Stats, error) {
	response, err := r.client.Get(r.baseURL + "/stats")
	if err != nil {
		return Stats{}, err
	}
	defer response.Body.Close()
	var stats Stats
	if err = json.NewDecoder(response.Body).Decode(&stats); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

// Reset zeroes the remote counters.
func (r *Remote) Reset() error {
	response, err := r.client.Post(r.baseURL+"/reset", "application/json", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return response.Body.Close()
}

// Close is a no-op: the process that hosts the target is owned by the runner.
func (r *Remote) Close() error { return nil }
