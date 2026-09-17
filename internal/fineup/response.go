package fineup

import (
	"encoding/json"
	"io"
	"net/http"
)

// maxResponseBody caps how much of an answer is read. A healthy answer is a
// short JSON object; a broken server can serve megabytes of HTML.
const maxResponseBody = 64 << 10

// parseResponse reads the answer of an upload server and returns the uuid it
// reported. Anything other than a 2xx carrying {"success":true,...} is a
// *ServerError.
//
// It closes the body, so callers do not have to remember to on every branch.
func parseResponse(resp *http.Response) (string, error) {
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &ServerError{Status: resp.StatusCode, Body: string(body)}
	}

	var answer struct {
		Success bool   `json:"success"`
		UUID    string `json:"uuid"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || !answer.Success {
		return "", &ServerError{Status: resp.StatusCode, Body: string(body)}
	}
	return answer.UUID, nil
}
