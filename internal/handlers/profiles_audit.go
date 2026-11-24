package handlers

// Garceslab Anime (phase 4): profile_audit entries written by the handlers
// (imports and exports; the profile service writes the rest).

import (
	"seanime/internal/profiles"
	"strings"

	"github.com/labstack/echo/v4"
)

// auditProfileImport records an import into p. The source comes from the route.
func (h *Handler) auditProfileImport(c echo.Context, p *profiles.Profile) {
	if h.App == nil || h.App.Profiles == nil {
		return
	}
	source := importAuditSource(c.Path())
	if s, ok := c.Get(importAuditSourceKey).(string); ok && s != "" {
		source = s // set by the handler (phase 3: token imports)
	}
	h.App.Profiles.RecordAudit(c.Request().Context(), p, profiles.AuditImport, source)
}

// importAuditSourceKey lets an import handler name its source more precisely.
const importAuditSourceKey = "profileImportAuditSource"

// importAuditSource names the import source of an import route.
func importAuditSource(route string) string {
	switch {
	case strings.HasSuffix(route, "/import/anilist"):
		return "Public AniList list"
	case strings.HasSuffix(route, "/import/server-account"):
		return "The server's AniList account"
	case strings.HasSuffix(route, "/import/json"):
		return "JSON file"
	}
	return ""
}
