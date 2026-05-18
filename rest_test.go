package cobrax

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildRESTHandler constructs an http.Handler with a single tool registered,
// using the provided toolMeta and a stub execFn that records the args it
// received and returns a fixed ToolOutput.
//
// The stub replaces execSubprocess so we can test the REST layer without
// spawning a real subprocess.
func buildRESTMux(toolName string, meta toolMeta) (*http.ServeMux, *[][]string) {
	var capturedArgs [][]string

	mux := http.NewServeMux()
	mux.HandleFunc("/"+toolName, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			restWriteError(w, http.StatusMethodNotAllowed,
				"method "+r.Method+" not allowed; use POST")
			return
		}

		var flat map[string]any
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&flat); err != nil {
			if err.Error() == "EOF" {
				flat = map[string]any{}
			} else {
				restWriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
				return
			}
		}

		argNames := make([]string, len(meta.argSpecs))
		for i, spec := range meta.argSpecs {
			argNames[i] = spec.Name
		}
		input := ToolInput{
			FlatInput: flat,
			FlagNames: meta.flagNames,
			ArgNames:  argNames,
		}

		args := buildCommandArgs(toolName, input)
		capturedArgs = append(capturedArgs, args)

		output := ToolOutput{StdOut: "ok", StdErr: "", ExitCode: 0}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(output)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[1:]
		restWriteError(w, http.StatusNotFound, `tool "`+name+`" not found`)
	})

	return mux, &capturedArgs
}

func TestRESTToolHandler_FlagsOnly(t *testing.T) {
	meta := toolMeta{
		flagNames: map[string]struct{}{
			"namespace": {},
			"output":    {},
			"verbose":   {},
		},
	}

	mux, captured := buildRESTMux("myapp_get", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := `{"namespace":"prod","output":"json","verbose":true}`
	resp, err := http.Post(srv.URL+"/myapp_get", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out ToolOutput
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "ok", out.StdOut)
	assert.Equal(t, 0, out.ExitCode)

	require.Len(t, *captured, 1)
	args := (*captured)[0]
	// Command segment: "get"
	assert.Equal(t, "get", args[0])
	// All three flags should be present.
	assert.Contains(t, args, "--namespace")
	assert.Contains(t, args, "prod")
	assert.Contains(t, args, "--output")
	assert.Contains(t, args, "json")
	assert.Contains(t, args, "--verbose")
}

func TestRESTToolHandler_FlagsAndPositionalArgs(t *testing.T) {
	meta := toolMeta{
		flagNames: map[string]struct{}{
			"level": {},
		},
		argSpecs: []ArgSpec{
			{Name: "module", Required: true, Index: 0},
			{Name: "title", Required: false, Index: 1},
		},
	}

	mux, captured := buildRESTMux("myapp_oncall", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := `{"module":"bke","title":"pod crash","level":2}`
	resp, err := http.Post(srv.URL+"/myapp_oncall", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, *captured, 1)
	args := (*captured)[0]

	// Command segment: "oncall"
	assert.Equal(t, "oncall", args[0])

	// Flag should appear.
	assert.Contains(t, args, "--level")
	assert.Contains(t, args, "2")

	// Positional args must appear in spec order (module before title).
	moduleIdx := indexOf(args, "bke")
	titleIdx := indexOf(args, "pod crash")
	require.NotEqual(t, -1, moduleIdx, "module value 'bke' not found in args")
	require.NotEqual(t, -1, titleIdx, "title value 'pod crash' not found in args")
	assert.Less(t, moduleIdx, titleIdx, "module must precede title in args")
}

func TestRESTToolHandler_EmptyBody(t *testing.T) {
	meta := toolMeta{
		flagNames: map[string]struct{}{"verbose": {}},
	}

	mux, captured := buildRESTMux("myapp_list", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Send request with no body at all.
	resp, err := http.Post(srv.URL+"/myapp_list", "application/json", http.NoBody)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// No panic; args should just be ["list"].
	require.Len(t, *captured, 1)
	assert.Equal(t, []string{"list"}, (*captured)[0])
}

func TestRESTToolHandler_InvalidJSON(t *testing.T) {
	meta := toolMeta{flagNames: map[string]struct{}{}}
	mux, _ := buildRESTMux("myapp_cmd", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/myapp_cmd", "application/json",
		bytes.NewBufferString("{not valid json"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "invalid JSON body")
}

func TestRESTToolHandler_WrongMethod(t *testing.T) {
	meta := toolMeta{flagNames: map[string]struct{}{}}
	mux, _ := buildRESTMux("myapp_cmd", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/myapp_cmd")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "not allowed")
}

func TestRESTToolHandler_NotFound(t *testing.T) {
	meta := toolMeta{flagNames: map[string]struct{}{}}
	mux, _ := buildRESTMux("myapp_cmd", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/nonexistent", "application/json",
		bytes.NewBufferString("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errResp))
	assert.Contains(t, errResp["error"], "not found")
}

func TestRESTToolHandler_BoolFlagFalseOmitted(t *testing.T) {
	meta := toolMeta{
		flagNames: map[string]struct{}{
			"verbose": {},
			"dry-run": {},
		},
	}

	mux, captured := buildRESTMux("myapp_deploy", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// verbose=false should be omitted; dry-run=true should appear as --dry-run.
	body := `{"verbose":false,"dry-run":true}`
	resp, err := http.Post(srv.URL+"/myapp_deploy", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, *captured, 1)
	args := (*captured)[0]

	assert.NotContains(t, args, "--verbose", "false bool flag must be omitted")
	assert.Contains(t, args, "--dry-run")
}

func TestRESTToolHandler_ArrayFlag(t *testing.T) {
	meta := toolMeta{
		flagNames: map[string]struct{}{"label": {}},
	}

	mux, captured := buildRESTMux("myapp_create", meta)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := `{"label":["env=prod","team=sre"]}`
	resp, err := http.Post(srv.URL+"/myapp_create", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, *captured, 1)
	args := (*captured)[0]

	// Repeated --label flags.
	assert.Equal(t, 2, countOccurrences(args, "--label"))
	assert.Contains(t, args, "env=prod")
	assert.Contains(t, args, "team=sre")
}

// indexOf returns the index of val in slice, or -1 if not found.
func indexOf(slice []string, val string) int {
	for i, s := range slice {
		if s == val {
			return i
		}
	}
	return -1
}

// countOccurrences returns the number of times val appears in slice.
func countOccurrences(slice []string, val string) int {
	count := 0
	for _, s := range slice {
		if s == val {
			count++
		}
	}
	return count
}
