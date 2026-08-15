package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// StreamToClient proxies Server-Sent Events from a provider channel to an HTTP client.
//
// Why this function exists: LLM responses can be very large (thousands of tokens).
// Without streaming, the client waits for the entire response to generate before
// seeing anything. With SSE streaming, the client sees tokens as they're generated,
// reducing perceived latency from seconds to milliseconds for the first token.
//
// The function never buffers the full response — each chunk is marshaled, written,
// and flushed individually. This keeps gateway memory usage constant regardless of
// response size.
//
// The error channel is drained after the chunks channel closes, because the provider
// goroutine sends the error and then closes both channels. If we checked mid-loop,
// we might see a spurious "no error" before the goroutine finishes.
func StreamToClient(w http.ResponseWriter, chunks <-chan providers.StreamChunk, errCh <-chan error) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported: ResponseWriter does not implement http.Flusher")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx buffering for SSE
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for chunk := range chunks {
		data, err := json.Marshal(chunk)
		if err != nil {
			// Log but don't abort — one bad chunk shouldn't kill the stream
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Drain the error channel. The provider goroutine closes errCh after
	// closing chunkCh, so by the time we reach here both channels are closed.
	// We still use select+default in case the error channel was never written to.
	var streamErr error
	select {
	case err, ok := <-errCh:
		if ok && err != nil {
			streamErr = err
		}
	default:
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	return streamErr
}
