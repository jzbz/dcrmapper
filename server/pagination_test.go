package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPaginationParams(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantFirst int
		wantLast  int
		wantErr   bool
	}{
		{"valid first page", "pageNumber=1&pageSize=10", 0, 10, false},
		{"valid second page", "pageNumber=2&pageSize=10", 10, 20, false},
		{"max page size", "pageNumber=1&pageSize=100", 0, 100, false},
		{"page size over max", "pageNumber=1&pageSize=101", 0, 0, true},
		{"zero page number", "pageNumber=0&pageSize=10", 0, 0, true},
		{"zero page size", "pageNumber=1&pageSize=0", 0, 0, true},
		{"negative page number", "pageNumber=-1&pageSize=10", 0, 0, true},
		{"non-numeric", "pageNumber=x&pageSize=10", 0, 0, true},
		{"missing params", "", 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/nodes?"+tc.query, nil)
			first, last, err := getPaginationParams(req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if first != tc.wantFirst || last != tc.wantLast {
				t.Errorf("got (%d, %d), want (%d, %d)", first, last, tc.wantFirst, tc.wantLast)
			}
		})
	}
}
