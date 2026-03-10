package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ContextSignature groups invocations by categorical dimensions only.
// No sensitive workspace data (paths, tokens, repo names) in the key.
type ContextSignature struct {
	TaskFamily       string `json:"task_family"`
	Lang             string `json:"lang"`
	ConstraintsClass string `json:"constraints_class"`
}

// Key returns a stable string key for use in maps and Valkey keys.
func (c ContextSignature) Key() string {
	return fmt.Sprintf("%s:%s:%s", c.TaskFamily, c.Lang, c.ConstraintsClass)
}

// PseudonymizeID returns an HMAC-SHA256 hash for pseudonymizing identifiers
// (user IDs, workspace IDs) in the telemetry lake.
func PseudonymizeID(tenantKey, rawID string) string {
	mac := hmac.New(sha256.New, []byte(tenantKey))
	mac.Write([]byte(rawID))
	return hex.EncodeToString(mac.Sum(nil))
}
