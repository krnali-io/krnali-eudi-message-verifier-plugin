// Package preset is the only source of credential queries accepted by the service.
package preset

import "encoding/json"

const Namespace = "eu.europa.ec.eudi.pid.1"

type Preset struct {
	ID      string          `json:"id"`
	Label   string          `json:"label"`
	Purpose string          `json:"purpose"`
	DCQL    json.RawMessage `json:"-"`
}

var registry = build()

func Get(id string) (Preset, bool) {
	p, ok := registry[id]
	p.DCQL = append(json.RawMessage(nil), p.DCQL...)
	return p, ok
}

func build() map[string]Preset {
	m := make(map[string]Preset)
	for _, id := range []string{"confirm-name", "confirm-name-sdjwt", "confirm-name-mdoc", "over-18"} {
		label, purpose := "Name", "Confirm your name"
		sdPaths := [][]string{{"given_name"}, {"family_name"}}
		mdPaths := [][]string{{Namespace, "given_name"}, {Namespace, "family_name"}}
		if id == "over-18" {
			label, purpose = "Over 18", "Confirm you are over 18"
			sdPaths = [][]string{{"age_equal_or_over", "18"}}
			mdPaths = [][]string{{Namespace, "age_over_18"}}
		}
		claims := func(paths [][]string) []map[string]any {
			out := []map[string]any{}
			for _, p := range paths {
				out = append(out, map[string]any{"path": p})
			}
			return out
		}
		sd := map[string]any{"id": "pid_sdjwt", "format": "dc+sd-jwt", "meta": map[string]any{"vct_values": []string{"urn:eudi:pid:1"}}, "claims": claims(sdPaths)}
		md := map[string]any{"id": "pid_mdoc", "format": "mso_mdoc", "meta": map[string]any{"doctype_value": Namespace}, "claims": claims(mdPaths)}
		q := map[string]any{"credentials": []any{sd, md}, "credential_sets": []any{map[string]any{"options": [][]string{{"pid_sdjwt"}, {"pid_mdoc"}}, "purpose": purpose}}}
		if id == "confirm-name-sdjwt" {
			q = map[string]any{"credentials": []any{sd}}
		}
		if id == "confirm-name-mdoc" {
			q = map[string]any{"credentials": []any{md}}
		}
		b, _ := json.Marshal(q)
		m[id] = Preset{ID: id, Label: label, Purpose: purpose, DCQL: b}
	}
	return m
}
