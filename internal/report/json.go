package report

import (
	"encoding/json"
	"io"

	"github.com/lighthousenz/scraps/internal/model"
)

// JSON writes the run report as JSON matching the README's example shape.
// Keys are kebab-case, results are in input order, key order is stable
// (ensured by encoding/json's sorted-key output on struct fields).
func JSON(w io.Writer, report *model.RunReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(report)
}
