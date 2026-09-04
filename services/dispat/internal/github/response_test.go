package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseLimitRejectsEvenAValidJSONPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		tooLarge   bool
	}{
		{"exact limit", "[]", false},
		{"valid JSON prefix with discarded tail", "[]{}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			r := &Releaser{Client: srv.Client()}
			data, status, err := r.do(context.Background(), apiCall{
				Method: http.MethodGet, URL: srv.URL, WantStatus: http.StatusOK,
				MaxBody: 2, What: "listing releases",
			})
			assert.Equal(t, http.StatusOK, status)
			if tc.tooLarge {
				require.ErrorContains(t, err, "response exceeds 2 bytes")
				assert.Nil(t, data, "partial listings must not reach release reconciliation")
			} else {
				require.NoError(t, err)
				assert.Equal(t, "[]", string(data))
			}
		})
	}
}
