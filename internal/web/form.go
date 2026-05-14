package web

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/filesystem"
)

// AlphaNumDashString normalizes user-entered slug fields during Gin form binding.
type AlphaNumDashString string

func (s *AlphaNumDashString) UnmarshalParam(value string) error {
	*s = AlphaNumDashString(filesystem.ToDirectoryName(value))
	return nil
}

func (s AlphaNumDashString) String() string {
	return string(s)
}

// CheckboxBool accepts browser checkbox values during Gin form binding.
type CheckboxBool bool

func (b *CheckboxBool) UnmarshalParam(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "yes", "y", "on":
		*b = true
	default:
		*b = false
	}
	return nil
}

func (b CheckboxBool) Bool() bool { return bool(b) }

func (s *Server) decodeForm(c *gin.Context, dst any, redirectBack string) bool {
	if err := c.ShouldBind(dst); err != nil {
		s.redirectWithFlash(c, redirectBack, "", "Please check the form and try again.")
		return false
	}
	return true
}
