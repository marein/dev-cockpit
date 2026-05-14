package web

import (
	"encoding/base64"
)

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
