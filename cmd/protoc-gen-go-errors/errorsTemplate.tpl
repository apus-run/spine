{{ range .Errors }}

{{ if .HasComment }}{{ .Comment }}{{ end -}}
func Is{{.CamelValue}}(err error) bool {
	if err == nil {
		return false
	}
	e := {{$.FromError}}(err)
	return e.Reason == {{ .GoValue }}.String() && e.Code == {{ .HTTPCode }}
}

{{ if .HasComment }}{{ .Comment }}{{ end -}}
func Error{{ .CamelValue }}(format string, args ...interface{}) *{{$.ErrorType}} {
	return {{$.NewError}}({{ .HTTPCode }}, {{ .GoValue }}.String(), {{$.Sprintf}}(format, args...))
}

{{- end }}
