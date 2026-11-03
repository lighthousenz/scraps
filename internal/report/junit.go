package report

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lighthousenz/scraps/internal/model"
)

type junitTestSuites struct {
	XMLName    xml.Name         `xml:"testsuites"`
	TestSuites []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitError   `xml:"error,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

type junitError struct {
	Message string `xml:"message,attr"`
}

// JUnit writes the run report as JUnit XML.
// A verdict in the failOn set becomes <failure>. broken is always <error>.
// Any other verdict passes, with the verdict in <system-out>.
func JUnit(w io.Writer, report *model.RunReport, failOn map[model.Verdict]bool) error {
	suite := junitTestSuite{
		Name:  "scraps",
		Tests: len(report.Results),
	}

	for _, r := range report.Results {
		tc := junitTestCase{
			Classname: r.ID,
			Name:      formatTestCaseName(r, report.Subjects),
		}

		switch {
		case r.Verdict == model.VerdictBroken:
			suite.Errors++
			tc.Error = &junitError{Message: r.Reason}
		case failOn[r.Verdict]:
			suite.Failures++
			tc.Failure = &junitFailure{
				Message: fmt.Sprintf("expected %s, got %s (%s)", r.Expected, r.Actual, r.Verdict),
			}
		default:
			tc.SystemOut = string(r.Verdict)
		}

		suite.Cases = append(suite.Cases, tc)
	}

	suites := junitTestSuites{TestSuites: []junitTestSuite{suite}}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(suites); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func formatTestCaseName(r model.ScenarioResult, subjects map[string]string) string {
	var b strings.Builder
	b.WriteString(r.ID)
	b.WriteString(" ")
	b.WriteString(r.Title)

	if len(subjects) > 0 {
		b.WriteString(" [")
		// Sorted subject names for stability.
		names := make([]string, 0, len(subjects))
		for name := range subjects {
			names = append(names, name)
		}
		sort.Strings(names)
		for i, name := range names {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(name)
			b.WriteString("=")
			b.WriteString(subjects[name])
		}
		b.WriteString("]")
	}

	return b.String()
}
