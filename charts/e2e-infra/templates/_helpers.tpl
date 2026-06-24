{{/* Common labels for every e2e-infra object. */}}
{{- define "e2e-infra.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: e2e-infra
{{- end -}}
