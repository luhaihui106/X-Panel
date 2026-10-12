package service

import "encoding/json"

// Changes to an email or protocol key need a quiescent core; Xray offers no
// atomic identity migration plus destructive counter acknowledgement.
func clientIdentitiesChanged(oldSettings, newSettings string) bool {
	identities := func(raw string) map[string]int {
		var settings struct {
			Clients []struct {
				Email    string `json:"email"`
				ID       string `json:"id"`
				Password string `json:"password"`
			} `json:"clients"`
		}
		if json.Unmarshal([]byte(raw), &settings) != nil {
			return nil
		}
		result := map[string]int{}
		for _, c := range settings.Clients {
			result[c.Email+"\x00"+c.ID+"\x00"+c.Password]++
		}
		return result
	}
	a, b := identities(oldSettings), identities(newSettings)
	if a == nil || b == nil || len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	return false
}
