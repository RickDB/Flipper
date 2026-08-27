package web

import (
	"embed"
	"fmt"
	"html"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

func loadTemplates() (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{
		"fmtTime":        fmtTime,
		"isAllowed":      isAllowed,
		"inIntSlice":     inIntSlice,
		"humanizeSize":   humanizeSize,
		"decodeEntities": html.UnescapeString,
	}).ParseFS(templateFS, "templates/*.html")
}

func isAllowed(cat string, allowed []string) bool {
	for _, a := range allowed {
		if a == cat {
			return true
		}
	}
	return false
}

func inIntSlice(id int, ids []int) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func humanizeSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
