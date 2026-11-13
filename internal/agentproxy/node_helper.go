package agentproxy

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// NodeHelperRuntime is the runtime name recorded in the workspace manifest for
// the embedded script. The same script runs on every workspace architecture, so
// there is no os/arch in a Node helper's identity.
const NodeHelperRuntime = "node"

// nodeHelperScript is the workspace side of the tunnel, written in Node and
// shipped inside the aha2 executable. It is uploaded into the workspace at run
// time, which is why it must not import anything outside the standard library.
//
//go:embed node_helper.js
var nodeHelperScript []byte

var nodeHelperDigest = func() string {
	sum := sha256.Sum256(nodeHelperScript)
	return hex.EncodeToString(sum[:])
}()

// NodeHelper returns the embedded script and its SHA-256. The slice is shared:
// callers must not modify it.
func NodeHelper() ([]byte, string) {
	return nodeHelperScript, nodeHelperDigest
}
