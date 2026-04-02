{{/*
Expand the name of the chart.
*/}}
{{- define "underpass-runtime.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "underpass-runtime.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "underpass-runtime.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "underpass-runtime.labels" -}}
helm.sh/chart: {{ include "underpass-runtime.chart" . }}
{{ include "underpass-runtime.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "underpass-runtime.selectorLabels" -}}
app.kubernetes.io/name: {{ include "underpass-runtime.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "underpass-runtime.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "underpass-runtime.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Safe TLS mode accessors — nil-safe when tls/natsTls/valkeyTls blocks are absent
(e.g. helm upgrade --reuse-values from a pre-TLS release).
*/}}
{{- define "underpass-runtime.tlsMode" -}}
{{- default "disabled" (default (dict) .Values.tls).mode -}}
{{- end -}}

{{- define "underpass-runtime.natsTlsMode" -}}
{{- default "disabled" (default (dict) .Values.natsTls).mode -}}
{{- end -}}

{{- define "underpass-runtime.valkeyTlsEnabled" -}}
{{- default false (default (dict) .Values.valkeyTls).enabled -}}
{{- end -}}

{{/*
Validate TLS configuration values (fail-fast guards).
Mirrors rehydration-kernel _helpers.tpl validation pattern.
*/}}
{{- define "underpass-runtime.validateValues" -}}
{{- $tls := default (dict) .Values.tls -}}
{{- $natsTls := default (dict) .Values.natsTls -}}
{{- $natsTlsKeys := default (dict) $natsTls.keys -}}
{{- $valkeyTls := default (dict) .Values.valkeyTls -}}
{{- $valkeyTlsKeys := default (dict) $valkeyTls.keys -}}
{{- $tlsMode := default "disabled" $tls.mode -}}
{{- $tlsKeys := default (dict) $tls.keys -}}
{{- $natsTlsMode := default "disabled" $natsTls.mode -}}
{{- $natsTlsSecret := default "" $natsTls.existingSecret -}}
{{- $natsTlsMountPath := default "" $natsTls.mountPath -}}
{{- $natsTlsCertKey := default "" $natsTlsKeys.cert -}}
{{- $natsTlsKeyKey := default "" $natsTlsKeys.key -}}
{{- $valkeyTlsEnabled := default false $valkeyTls.enabled -}}
{{- $valkeyTlsSecret := default "" $valkeyTls.existingSecret -}}
{{- $valkeyTlsMountPath := default "" $valkeyTls.mountPath -}}
{{- $valkeyTlsCaKey := default "" $valkeyTlsKeys.ca -}}
{{- $valkeyTlsCertKey := default "" $valkeyTlsKeys.cert -}}
{{- $valkeyTlsKeyKey := default "" $valkeyTlsKeys.key -}}
{{/* --- HTTP server TLS --- */}}
{{- if not (has $tlsMode (list "disabled" "server" "mutual")) -}}
{{- fail "tls.mode must be one of disabled, server, mutual" -}}
{{- end -}}
{{- if ne $tlsMode "disabled" -}}
{{- if eq (default "" $tls.existingSecret) "" -}}
{{- fail "tls.existingSecret is required when tls.mode is server or mutual" -}}
{{- end -}}
{{- if eq (default "" $tls.mountPath) "" -}}
{{- fail "tls.mountPath is required when tls.mode is server or mutual" -}}
{{- end -}}
{{- if eq (default "" $tlsKeys.cert) "" -}}
{{- fail "tls.keys.cert is required when tls.mode is server or mutual" -}}
{{- end -}}
{{- if eq (default "" $tlsKeys.key) "" -}}
{{- fail "tls.keys.key is required when tls.mode is server or mutual" -}}
{{- end -}}
{{- if and (eq $tlsMode "mutual") (eq (default "" $tlsKeys.clientCa) "") -}}
{{- fail "tls.keys.clientCa is required when tls.mode=mutual" -}}
{{- end -}}
{{- end -}}
{{/* --- NATS TLS --- */}}
{{- if not (has $natsTlsMode (list "disabled" "server" "mutual")) -}}
{{- fail "natsTls.mode must be one of disabled, server, mutual" -}}
{{- end -}}
{{- if and (ne $natsTlsMode "disabled") (eq $natsTlsSecret "") (or (ne $natsTlsCertKey "") (ne $natsTlsKeyKey "")) -}}
{{- fail "natsTls.existingSecret is required when natsTls.keys.* are configured" -}}
{{- end -}}
{{- if and (ne $natsTlsSecret "") (eq $natsTlsMountPath "") -}}
{{- fail "natsTls.mountPath is required when natsTls.existingSecret is set" -}}
{{- end -}}
{{- if and (eq $natsTlsMode "mutual") (eq $natsTlsSecret "") -}}
{{- fail "natsTls.existingSecret is required when natsTls.mode=mutual" -}}
{{- end -}}
{{- if and (eq $natsTlsMode "mutual") (or (eq $natsTlsCertKey "") (eq $natsTlsKeyKey "")) -}}
{{- fail "natsTls.keys.cert and natsTls.keys.key are required when natsTls.mode=mutual" -}}
{{- end -}}
{{- if and (or (eq $natsTlsCertKey "") (eq $natsTlsKeyKey "")) (not (and (eq $natsTlsCertKey "") (eq $natsTlsKeyKey ""))) -}}
{{- fail "natsTls.keys.cert and natsTls.keys.key must be configured together" -}}
{{- end -}}
{{/* --- Valkey TLS --- */}}
{{- if and $valkeyTlsEnabled (eq $valkeyTlsSecret "") (or (ne $valkeyTlsCaKey "") (ne $valkeyTlsCertKey "") (ne $valkeyTlsKeyKey "")) -}}
{{- fail "valkeyTls.existingSecret is required when valkeyTls.keys.* are configured" -}}
{{- end -}}
{{- if and (ne $valkeyTlsSecret "") (eq $valkeyTlsMountPath "") -}}
{{- fail "valkeyTls.mountPath is required when valkeyTls.existingSecret is set" -}}
{{- end -}}
{{- if and (or (eq $valkeyTlsCertKey "") (eq $valkeyTlsKeyKey "")) (not (and (eq $valkeyTlsCertKey "") (eq $valkeyTlsKeyKey ""))) -}}
{{- fail "valkeyTls.keys.cert and valkeyTls.keys.key must be configured together" -}}
{{- end -}}
{{- end -}}

{{/*
Resolve the CA secret name used by the cert-gen hook Job.
*/}}
{{- define "underpass-runtime.certGen.caSecretName" -}}
{{- default "rehydration-kernel-internal-ca" .Values.certGen.caSecret -}}
{{- end -}}
