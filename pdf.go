package raven

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"os/exec"
)

//go:embed templates/investigation_report.html
var investigationReportHTML string

//go:embed templates/investigation_history.html
var investigationHistoryHTML string

//go:embed templates/investigation_report.css
var investigationReportCSS string

//go:embed templates/investigation_history.css
var investigationHistoryCSS string

// TODO:we dockerize our app
func generatePDF(htmlFileName string, template *template.Template, payload any, tempDir string) (string, error) {

	htmlFile, err := os.CreateTemp(tempDir, htmlFileName)
	if err != nil {
		return "", err
	}
	defer os.Remove(htmlFile.Name())

	err = template.Execute(htmlFile, payload)
	if err != nil {
		htmlFile.Close()
		return "", err
	}

	htmlFile.Close()
	
	pdf, err := os.CreateTemp(tempDir, "pdf-*.pdf")
	if err != nil {
		return "", err
	}
	// caller's responsibility do delete the temporary pdf
	defer pdf.Close()

	cmd := exec.Command(
		"prince",
		htmlFile.Name(),
		"-o",
		pdf.Name(),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"princeXML failed: %w\n%s",
			err,
			string(output),
		)
	}

	return pdf.Name(), nil
}

