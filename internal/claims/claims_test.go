package claims

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func enc(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
func disclosure(v any) (string, string) {
	s := enc(v)
	h := sha256.Sum256([]byte(s))
	return s, base64.RawURLEncoding.EncodeToString(h[:])
}
func TestNestedSDJWTAndProjection(t *testing.T) {
	age, ageHash := disclosure([]any{"salt", "18", false})
	nested, nestedHash := disclosure([]any{"salt2", "age_equal_or_over", map[string]any{"_sd": []string{ageHash}}})
	given, givenHash := disclosure([]any{"salt3", "given_name", "Anna"})
	payload := map[string]any{"vct": "urn:eudi:pid:1", "family_name": "Larsen", "_sd_alg": "sha-256", "_sd": []string{nestedHash, givenHash}}
	vp := enc(map[string]string{"alg": "ES256"}) + "." + enc(payload) + ".signature~" + age + "~" + nested + "~" + given + "~kb.jwt.signature"
	r, e := SDJWT(vp, "over-18")
	if e != nil || r.AgeOver18 == nil || *r.AgeOver18 || r.GivenName != "" {
		t.Fatalf("age result: %+v %v", r, e)
	}
	r, e = SDJWT(vp, "confirm-name")
	if e != nil || r.GivenName != "Anna" || r.FamilyName != "Larsen" || r.AgeOver18 != nil {
		t.Fatalf("name result: %+v %v", r, e)
	}
	if !Matches("  ANNA \t larsen ", r) || Matches("Alice Larsen", r) {
		t.Fatal("name check")
	}
}
func TestSDJWTRejectsMissingWrongAndCollision(t *testing.T) {
	for _, obj := range []map[string]any{{"vct": "wrong", "given_name": "Anna", "family_name": "Larsen"}, {"vct": "urn:eudi:pid:1", "given_name": "Anna"}, {"vct": "urn:eudi:pid:1", "given_name": 17, "family_name": "Larsen"}, {"vct": "urn:eudi:pid:1", "_sd_alg": "sha-512", "given_name": "Anna", "family_name": "Larsen"}} {
		if _, e := SDJWT("h."+enc(obj)+".s~", "confirm-name"); e == nil {
			t.Fatal("accepted invalid claims")
		}
	}
	d, h := disclosure([]any{"salt", "given_name", "Mallory"})
	if _, e := SDJWT("h."+enc(map[string]any{"vct": "urn:eudi:pid:1", "given_name": "Anna", "family_name": "Larsen", "_sd": []string{h}})+".s~"+d+"~", "confirm-name"); e == nil {
		t.Fatal("accepted collision")
	}
}
func TestMDocProjection(t *testing.T) {
	d := []Document{{DocType: "eu.europa.ec.eudi.pid.1", Attributes: map[string]map[string]any{"eu.europa.ec.eudi.pid.1": {"given_name": "Anna", "family_name": "Larsen", "age_over_18": true}}}}
	r, e := MDoc(d, "confirm-name")
	if e != nil || r.GivenName != "Anna" || r.AgeOver18 != nil {
		t.Fatal(r, e)
	}
	r, e = MDoc(d, "over-18")
	if e != nil || r.AgeOver18 == nil || !*r.AgeOver18 || r.GivenName != "" {
		t.Fatal(r, e)
	}
	if _, e = MDoc(append(d, d...), "confirm-name"); e == nil {
		t.Fatal("ambiguous documents accepted")
	}
}
