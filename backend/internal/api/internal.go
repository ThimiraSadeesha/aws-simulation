package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"cloudsim/backend/internal/auth"
)

const keyInternalPrincipal ctxKey = 50

// InternalResponse is the result of an in-process API call.
type InternalResponse struct {
	Status int
	Body   []byte
	Header http.Header
}

// Internal executes an API request in-process as principal p, going through exactly the same
// routing, IAM authorization, validation and audit path as an external request. It is used by
// the web terminal (cloudsim commands) and by training-lab validators. No shell is involved.
func (s *Server) Internal(ctx context.Context, p *auth.Principal, method, path string, body any, headers map[string]string) *InternalResponse {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rd).WithContext(context.WithValue(ctx, keyInternalPrincipal, p))
	req.RemoteAddr = "127.0.0.1:0"
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return &InternalResponse{Status: rec.Code, Body: rec.Body.Bytes(), Header: rec.Header()}
}

// PrincipalForAccount builds the root principal of an account (used by background controllers).
func (s *Server) PrincipalForAccount(ctx context.Context, account string) (*auth.Principal, error) {
	u, err := s.Store.Users.GetByAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return &auth.Principal{Kind: auth.KindRoot, AccountID: u.AccountID, PlatformUser: u, Name: u.Username, ARN: "arn:cloudsim:iam::" + u.AccountID + ":root"}, nil
}
