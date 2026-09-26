// Package claims decodes presentations already validated by the reference verifier.
// It MUST NOT be used as a credential verifier.
package claims

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/preset"
)

var ErrClaims = errors.New("invalid_claims")

type Result struct {
	GivenName  string `json:"given_name,omitempty"`
	FamilyName string `json:"family_name,omitempty"`
	AgeOver18  *bool  `json:"age_over_18,omitempty"`
	Format     string `json:"format"`
}

// Normalize deliberately does not perform NFC; the service stays stdlib-only.
func Normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
func Matches(expected string, r Result) bool {
	return Normalize(expected) == Normalize(r.GivenName+" "+r.FamilyName)
}

func project(obj map[string]any, id, format string) (Result, error) {
	r := Result{Format: format}
	if id == "over-18" {
		v, ok := obj["age_over_18"].(bool)
		if !ok {
			return Result{}, ErrClaims
		}
		r.AgeOver18 = &v
	} else {
		var ok bool
		r.GivenName, ok = obj["given_name"].(string)
		if !ok || strings.TrimSpace(r.GivenName) == "" || len(r.GivenName) > 512 {
			return Result{}, ErrClaims
		}
		r.FamilyName, ok = obj["family_name"].(string)
		if !ok || strings.TrimSpace(r.FamilyName) == "" || len(r.FamilyName) > 512 {
			return Result{}, ErrClaims
		}
	}
	return r, nil
}

func SDJWT(presentation, id string) (Result, error) {
	if len(presentation) > 1<<20 {
		return Result{}, ErrClaims
	}
	parts := strings.Split(presentation, "~")
	if len(parts) < 2 {
		return Result{}, ErrClaims
	}
	jwt := strings.Split(parts[0], ".")
	if len(jwt) != 3 {
		return Result{}, ErrClaims
	}
	b, err := base64.RawURLEncoding.DecodeString(jwt[1])
	if err != nil {
		return Result{}, ErrClaims
	}
	var payload map[string]any
	if json.Unmarshal(b, &payload) != nil || payload == nil {
		return Result{}, ErrClaims
	}
	if alg, ok := payload["_sd_alg"]; ok && alg != "sha-256" {
		return Result{}, ErrClaims
	}
	disclosures := map[string][]any{}
	for _, s := range parts[1:] {
		if s == "" || strings.Contains(s, ".") {
			continue
		}
		b, err = base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return Result{}, ErrClaims
		}
		var d []any
		if json.Unmarshal(b, &d) != nil || (len(d) != 2 && len(d) != 3) {
			return Result{}, ErrClaims
		}
		if _, ok := d[0].(string); !ok {
			return Result{}, ErrClaims
		}
		h := sha256.Sum256([]byte(s))
		key := base64.RawURLEncoding.EncodeToString(h[:])
		if _, ok := disclosures[key]; ok {
			return Result{}, ErrClaims
		}
		disclosures[key] = d
	}
	used := map[string]bool{}
	var walk func(any, int) (any, error)
	resolve := func(digest string, n int) ([]any, error) {
		d, ok := disclosures[digest]
		if !ok {
			return nil, nil
		}
		if used[digest] || len(d) != n {
			return nil, ErrClaims
		}
		used[digest] = true
		return d, nil
	}
	walk = func(v any, depth int) (any, error) {
		if depth > 64 {
			return nil, ErrClaims
		}
		switch x := v.(type) {
		case map[string]any:
			if raw, ok := x["_sd"]; ok {
				digests, ok := raw.([]any)
				if !ok {
					return nil, ErrClaims
				}
				for _, rawDigest := range digests {
					digest, ok := rawDigest.(string)
					if !ok {
						return nil, ErrClaims
					}
					d, e := resolve(digest, 3)
					if e != nil {
						return nil, e
					}
					if d == nil {
						continue
					}
					name, ok := d[1].(string)
					if !ok || name == "_sd" || name == "..." {
						return nil, ErrClaims
					}
					if _, exists := x[name]; exists {
						return nil, ErrClaims
					}
					x[name] = d[2]
				}
				delete(x, "_sd")
			}
			for k, v := range x {
				v, e := walk(v, depth+1)
				if e != nil {
					return nil, e
				}
				x[k] = v
			}
			return x, nil
		case []any:
			out := []any{}
			for _, v := range x {
				if m, ok := v.(map[string]any); ok {
					if raw, exists := m["..."]; exists {
						digest, ok := raw.(string)
						if !ok || len(m) != 1 {
							return nil, ErrClaims
						}
						d, e := resolve(digest, 2)
						if e != nil {
							return nil, e
						}
						if d == nil {
							continue
						}
						v = d[1]
					}
				}
				v, e := walk(v, depth+1)
				if e != nil {
					return nil, e
				}
				out = append(out, v)
			}
			return out, nil
		default:
			return v, nil
		}
	}
	if _, err = walk(payload, 0); err != nil {
		return Result{}, err
	}
	if payload["vct"] != "urn:eudi:pid:1" {
		return Result{}, ErrClaims
	}
	if age, ok := payload["age_equal_or_over"].(map[string]any); ok {
		payload["age_over_18"] = age["18"]
	}
	return project(payload, id, "dc+sd-jwt")
}

type Document struct {
	DocType    string                    `json:"docType"`
	Attributes map[string]map[string]any `json:"attributes"`
}

func MDoc(docs []Document, id string) (Result, error) {
	var found *Document
	for i := range docs {
		if docs[i].DocType == preset.Namespace {
			if found != nil {
				return Result{}, ErrClaims
			}
			found = &docs[i]
		}
	}
	if found == nil {
		return Result{}, ErrClaims
	}
	return project(found.Attributes[preset.Namespace], id, "mso_mdoc")
}
