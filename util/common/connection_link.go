package common

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
)

// VMessLink serializes the existing subscription JSON payload.
func VMessLink(payload map[string]any) string {
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

// VLESSLink serializes the existing subscription URL and remark.
func VLESSLink(id, address string, port int, params map[string]string, remark string) string {
	u, _ := url.Parse(fmt.Sprintf("vless://%s@%s:%d", id, address, port))
	q := u.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	u.RawQuery = q.Encode()
	u.Fragment = remark
	return u.String()
}
