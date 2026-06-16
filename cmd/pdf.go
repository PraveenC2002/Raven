package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"time"
)

//go:embed templates/investigation_report.html
var investigationReportHTML string

//go:embed templates/investigation_history.html
var investigationHistoryHTML string

//go:embed templates/investigation_report.css
var investigationReportCSS string

//go:embed templates/investigation_history.css
var investigationHistoryCSS string

// we dockerize our app
func htmlToPDF(htmlPath string, appConf *config) (string, error) {

	pdf, err := os.CreateTemp(appConf.tempDir, "pdf-*.pdf")
	if err != nil {
		return "", err
	}
	// caller's responsibility do delete the temporary pdf
	defer pdf.Close()

	cmd := exec.Command(
		"prince",
		htmlPath,
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

func generatePDF(content any, appConf *config) (string, error) {

	funcMap := template.FuncMap{
		"generatedTime": func() string {
			return time.Now().Format("2006-01-02 15:04:05")
		},
	}

	var payload any
	var tmpl *template.Template
	var htmlFileName string

	switch v := content.(type) {
	case *diagnosisReport:
		tmpl = template.Must(template.New("investigation report html").Funcs(funcMap).Parse(investigationReportHTML))
		payload = struct {
			*diagnosisReport
			CSS string
		}{
			diagnosisReport: v,
			CSS:             investigationReportCSS,
		}
		htmlFileName = "investigation-report-*.html"

	case *investigationHistory:
		tmpl = template.Must(template.New("investigation history html").Funcs(funcMap).Parse(investigationHistoryHTML))
		payload = struct {
			*investigationHistory
			CSS string
		}{
			investigationHistory: v,
			CSS:                  investigationHistoryCSS,
		}
		htmlFileName = "investigation-history-*.html"

	default:
		return "", fmt.Errorf("unknown content type")
	}

	htmlFile, err := os.CreateTemp(appConf.tempDir, htmlFileName)
	if err != nil {
		return "", err
	}
	defer os.Remove(htmlFile.Name())

	err = tmpl.Execute(htmlFile, payload)
	if err != nil {
		htmlFile.Close()
		return "", err
	}

	htmlFile.Close()
	return htmlToPDF(htmlFile.Name(), appConf)
}

