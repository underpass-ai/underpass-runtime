{{/*
Shared pod plumbing for the tool-learning CronJobs.

Both the policy-computation job (cronjob-tool-learning.yaml) and the export-lake
producer (cronjob-tool-learning-export.yaml) talk to the same Valkey, S3/MinIO
lake and TLS material, so the environment and TLS volumes are defined once here
to keep producer and consumer in sync. Call with the root context, e.g.
  env:
    {{- include "underpass-runtime.toolLearning.env" $ | nindent 16 }}
*/}}
{{- define "underpass-runtime.toolLearning.env" -}}
- name: HOME
  value: /tmp
- name: LOG_LEVEL
  value: {{ .Values.toolLearning.logLevel | quote }}
{{- /* S3 / MinIO */}}
- name: S3_ENDPOINT
  value: {{ .Values.toolLearning.s3.endpoint | quote }}
- name: S3_REGION
  value: {{ .Values.toolLearning.s3.region | quote }}
- name: S3_USE_SSL
  value: {{ or .Values.toolLearning.s3.useSSL (default false .Values.s3Tls.enabled) | quote }}
- name: LAKE_BUCKET
  value: {{ .Values.toolLearning.s3.lakeBucket | quote }}
- name: AUDIT_BUCKET
  value: {{ .Values.toolLearning.s3.auditBucket | quote }}
{{- if .Values.toolLearning.s3.existingSecret }}
- name: S3_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.toolLearning.s3.existingSecret }}
      key: {{ .Values.toolLearning.s3.accessKeyKey }}
- name: S3_SECRET_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.toolLearning.s3.existingSecret }}
      key: {{ .Values.toolLearning.s3.secretKeyKey }}
{{- end }}
{{- /* S3 TLS */}}
{{- if default false .Values.s3Tls.enabled }}
{{- if and .Values.s3Tls.existingSecret .Values.s3Tls.keys.ca }}
- name: S3_CA_PATH
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.ca | quote }}
{{- end }}
{{- if and .Values.s3Tls.keys.cert .Values.s3Tls.keys.key }}
- name: S3_CERT_PATH
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.cert | quote }}
- name: S3_KEY_PATH
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.key | quote }}
{{- end }}
{{- end }}
{{- /* Valkey */}}
- name: VALKEY_ADDR
  value: "{{ .Values.toolLearning.valkey.host }}:{{ .Values.toolLearning.valkey.port }}"
- name: VALKEY_DB
  value: {{ .Values.toolLearning.valkey.db | quote }}
- name: VALKEY_KEY_PREFIX
  value: {{ .Values.toolLearning.valkey.keyPrefix | quote }}
- name: VALKEY_TTL
  value: {{ .Values.toolLearning.valkey.ttl | quote }}
- name: VALKEY_TELEMETRY_PREFIX
  value: {{ .Values.toolLearning.valkey.telemetryPrefix | quote }}
{{- if .Values.toolLearning.valkey.existingSecret }}
- name: VALKEY_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.toolLearning.valkey.existingSecret }}
      key: {{ .Values.toolLearning.valkey.passwordKey | default "password" }}
{{- end }}
{{- /* NATS */}}
- name: NATS_URL
  value: {{ .Values.toolLearning.nats.url | quote }}
{{- /* NATS TLS */}}
{{- if ne (default "disabled" .Values.natsTls.mode) "disabled" }}
- name: NATS_TLS_MODE
  value: {{ .Values.natsTls.mode | quote }}
{{- if and .Values.natsTls.existingSecret .Values.natsTls.keys.ca }}
- name: NATS_TLS_CA_PATH
  value: {{ printf "%s/%s" .Values.natsTls.mountPath .Values.natsTls.keys.ca | quote }}
{{- end }}
{{- if and .Values.natsTls.keys.cert .Values.natsTls.keys.key }}
- name: NATS_TLS_CERT_PATH
  value: {{ printf "%s/%s" .Values.natsTls.mountPath .Values.natsTls.keys.cert | quote }}
- name: NATS_TLS_KEY_PATH
  value: {{ printf "%s/%s" .Values.natsTls.mountPath .Values.natsTls.keys.key | quote }}
{{- end }}
{{- end }}
{{- /* Valkey TLS */}}
{{- if .Values.valkeyTls.enabled }}
- name: VALKEY_TLS_ENABLED
  value: "true"
{{- if and .Values.valkeyTls.existingSecret .Values.valkeyTls.keys.ca }}
- name: VALKEY_TLS_CA_PATH
  value: {{ printf "%s/%s" .Values.valkeyTls.mountPath .Values.valkeyTls.keys.ca | quote }}
{{- end }}
{{- if and .Values.valkeyTls.keys.cert .Values.valkeyTls.keys.key }}
- name: VALKEY_TLS_CERT_PATH
  value: {{ printf "%s/%s" .Values.valkeyTls.mountPath .Values.valkeyTls.keys.cert | quote }}
- name: VALKEY_TLS_KEY_PATH
  value: {{ printf "%s/%s" .Values.valkeyTls.mountPath .Values.valkeyTls.keys.key | quote }}
{{- end }}
{{- end }}
{{- /* DuckDB/libcurl custom CA + client cert for S3 mTLS */}}
{{- if and (default false .Values.s3Tls.enabled) .Values.s3Tls.existingSecret .Values.s3Tls.keys.ca }}
- name: SSL_CERT_FILE
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.ca | quote }}
{{- if and .Values.s3Tls.keys.cert .Values.s3Tls.keys.key }}
- name: CURL_SSLCERT
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.cert | quote }}
- name: CURL_SSLKEY
  value: {{ printf "%s/%s" .Values.s3Tls.mountPath .Values.s3Tls.keys.key | quote }}
{{- end }}
{{- else if and .Values.valkeyTls.enabled .Values.valkeyTls.existingSecret .Values.valkeyTls.keys.ca }}
{{- /* Fallback: reuse Valkey CA for DuckDB/libcurl when s3Tls is not configured */}}
- name: SSL_CERT_FILE
  value: {{ printf "%s/%s" .Values.valkeyTls.mountPath .Values.valkeyTls.keys.ca | quote }}
{{- end }}
{{- end -}}

{{- define "underpass-runtime.toolLearning.volumeMounts" -}}
- name: tmp
  mountPath: /tmp
{{- if and .Values.valkeyTls.enabled .Values.valkeyTls.existingSecret }}
- name: valkey-tls
  mountPath: {{ .Values.valkeyTls.mountPath }}
  readOnly: true
{{- end }}
{{- if and (ne (default "disabled" .Values.natsTls.mode) "disabled") .Values.natsTls.existingSecret }}
- name: nats-tls
  mountPath: {{ .Values.natsTls.mountPath }}
  readOnly: true
{{- end }}
{{- if and (default false .Values.s3Tls.enabled) .Values.s3Tls.existingSecret }}
- name: s3-tls
  mountPath: {{ .Values.s3Tls.mountPath }}
  readOnly: true
{{- end }}
{{- end -}}

{{- define "underpass-runtime.toolLearning.volumes" -}}
- name: tmp
  emptyDir:
    sizeLimit: 256Mi
{{- if and .Values.valkeyTls.enabled .Values.valkeyTls.existingSecret }}
- name: valkey-tls
  secret:
    secretName: {{ .Values.valkeyTls.existingSecret }}
{{- end }}
{{- if and (ne (default "disabled" .Values.natsTls.mode) "disabled") .Values.natsTls.existingSecret }}
- name: nats-tls
  secret:
    secretName: {{ .Values.natsTls.existingSecret }}
{{- end }}
{{- if and (default false .Values.s3Tls.enabled) .Values.s3Tls.existingSecret }}
- name: s3-tls
  secret:
    secretName: {{ .Values.s3Tls.existingSecret }}
{{- end }}
{{- end -}}
