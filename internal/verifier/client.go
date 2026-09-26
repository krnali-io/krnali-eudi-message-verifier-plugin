package verifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
)

var ErrPending = errors.New("presentation_pending")
var ErrCode = errors.New("invalid_response_code")
var ErrVerifier = errors.New("verifier_error")

type Init struct {
	DCQL                    json.RawMessage `json:"dcql_query"`
	Nonce                   string          `json:"nonce"`
	ResponseMode            string          `json:"response_mode"`
	JARMode                 string          `json:"jar_mode"`
	RequestURIMethod        string          `json:"request_uri_method"`
	RedirectTemplate        string          `json:"wallet_response_redirect_uri_template"`
	IssuerChain             string          `json:"issuer_chain"`
	Scheme                  string          `json:"authorization_request_scheme"`
	Profile                 string          `json:"profile"`
	RegistrationCertificate string          `json:"registration_certificate,omitempty"`
}
type Transaction struct {
	ID               string `json:"transaction_id"`
	AuthorizationURI string `json:"authorization_request_uri"`
}
type Response struct {
	VPToken map[string][]string `json:"vp_token"`
	Error   string              `json:"error"`
}
type Service interface {
	Start(context.Context, Init) (Transaction, error)
	Response(context.Context, string, string) (Response, error)
	DecodeMDoc(context.Context, string, string) ([]claims.Document, error)
}
type Client struct {
	Base string
	HTTP *http.Client
	// The hosted EUDI service returns an empty 400 while a presentation is pending.
	// This compatibility path never accepts a presentation or its claims.
	EmptyPendingResponse bool
}

func New(base string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path, contentType string, body io.Reader) ([]byte, int, error) {
	r, e := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if e != nil {
		return nil, 0, ErrVerifier
	}
	r.Header.Set("Accept", "application/json")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, 0, ErrVerifier
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return nil, 0, ErrVerifier
	}
	return b, res.StatusCode, nil
}
func (c *Client) Start(ctx context.Context, in Init) (Transaction, error) {
	b, _ := json.Marshal(in)
	b, code, e := c.request(ctx, "POST", "/ui/presentations/v2", "application/json", bytes.NewReader(b))
	var out Transaction
	if e != nil || code < 200 || code >= 300 || json.Unmarshal(b, &out) != nil || out.ID == "" || len(out.ID) > 256 {
		return out, ErrVerifier
	}
	u, e := url.Parse(out.AuthorizationURI)
	if e != nil || (u.Scheme != "haip-vp" && u.Scheme != "openid4vp") || u.RawQuery == "" {
		return out, ErrVerifier
	}
	return out, nil
}
func (c *Client) Response(ctx context.Context, id, code string) (Response, error) {
	path := "/ui/presentations/" + url.PathEscape(id)
	if code != "" {
		path += "?" + url.Values{"response_code": {code}}.Encode()
	}
	b, status, e := c.request(ctx, "GET", path, "", nil)
	var out Response
	if e == nil && c.EmptyPendingResponse && status == http.StatusBadRequest && len(bytes.TrimSpace(b)) == 0 {
		if code != "" {
			return out, ErrCode
		}
		return out, ErrPending
	}
	if e != nil || json.Unmarshal(b, &out) != nil {
		return out, ErrVerifier
	}
	if status == 400 && out.Error == "PresentationNotSubmitted" {
		return out, ErrPending
	}
	if status == 400 && out.Error == "InvalidResponseCode" {
		return out, ErrCode
	}
	if status != 200 {
		return out, ErrVerifier
	}
	return out, nil
}
func (c *Client) DecodeMDoc(ctx context.Context, vp, chain string) ([]claims.Document, error) {
	b, code, e := c.request(ctx, "POST", "/utilities/validations/msoMdoc/deviceResponse", "application/x-www-form-urlencoded", strings.NewReader(url.Values{"device_response": {vp}, "issuer_chain": {chain}}.Encode()))
	var docs []claims.Document
	if e != nil || code != 200 || json.Unmarshal(b, &docs) != nil {
		return nil, ErrVerifier
	}
	return docs, nil
}
