# Auditoria de recomendacion y aprendizaje de herramientas en `underpass-runtime`

Fecha: 2026-04-02

## Alcance

Este documento audita la implementacion real de `underpass-runtime` para responder a una pregunta concreta de producto:

- que hace hoy el runtime online al recomendar herramientas
- que hace hoy el pipeline offline de `tool-learning`
- que partes se pueden monitorizar honestamente en la demo
- que huecos existen entre la arquitectura documentada y el codigo ejecutable

La auditoria se ha hecho contra el repo hermano local `underpass-runtime`, no contra slides ni notas de arquitectura.

## Veredicto corto

La conclusion importante es esta:

- el runtime online ya expone telemetria util y recomendaciones operativas
- el servicio `tool-learning` ya calcula politicas contextuales offline y las persiste en Valkey
- pero hoy no hay evidencia de que el runtime online consuma esas politicas aprendidas para `RecommendTools`
- y el path activo del runtime no incorpora hoy algoritmos SOTA de seleccion contextual; las piezas mas avanzadas viven como baseline research, benchmark o trabajo pendiente de integracion

Desde perspectiva estrictamente tecnica, esto describe el estado actual.
Desde perspectiva de producto, y dado que quieres que aprendizaje y descubrimiento
de tools sean un diferenciador central de `underpass-runtime`, la ausencia de
algoritmos SOTA en el path activo debe tratarse como **gap estrategico**, no
como deuda menor o mejora futura opcional.

Para la demo, eso obliga a separar dos paneles conceptuales:

- `runtime recommendation`: ranking heuristico + ajuste por telemetria agregada por herramienta
- `tool learning`: pipeline batch/offline que calcula politicas contextuales y las publica en Valkey/NATS/S3

Si se presenta como un unico loop cerrado de aprendizaje online en tiempo real, la demo exageraria el estado real del sistema.

## Arquitectura observada

La implementacion actual se parece a esto:

```text
Invocacion en runtime
  -> recordTelemetry() en runtime
  -> telemetria cruda en Valkey por herramienta
  -> agregador online por herramienta
  -> RecommendTools() usa heuristica + stats agregadas por herramienta

Tool-learning batch
  -> lee lake Parquet en S3/MinIO via DuckDB
  -> calcula politicas Thompson por (context_signature, tool_id)
  -> escribe tool_policy:* en Valkey
  -> publica tool_learning.policy.updated en NATS
  -> guarda snapshot de auditoria en S3
```

La brecha es esta:

```text
runtime online recommendations
    X
    no evidencia de consumo de tool_policy:* ni de invalidacion por tool_learning.policy.updated
```

## Metodo y busquedas relevantes

Para evitar confundir intencion con implementacion, se contrastaron:

- claims de README, ADR y contratos
- codigo online en `internal/` y `cmd/workspace`
- codigo offline en `services/tool-learning/`
- endpoints OpenAPI
- wiring Helm y observabilidad

Busquedas clave:

- `RecommendTools`
- `tool_learning.policy.updated`
- `ReadPolicy`
- `tool_policy`
- `context_signature`
- `BuildContextDigest`
- `ObserveRecommendationUsed`

## Hallazgos

### 1. Alta: el runtime online no consume las politicas aprendidas de `tool-learning`

#### Evidencia

- El README afirma que el agente "selects the best tools via Thompson Sampling" en `underpass-runtime/README.md:16` y `underpass-runtime/README.md:20`.
- El contrato compartido dice que el workspace consume politicas desde Valkey y eventos de politica desde NATS en `underpass-runtime/specs/tool-policy/v1/contract.md:130` a `underpass-runtime/specs/tool-policy/v1/contract.md:135`.
- La implementacion real de `RecommendTools()` usa solo:
  - `ListTools()`
  - tokenizacion de `task_hint`
  - heuristica estatica de riesgo/coste/side effects
  - boost/penalty por telemetria agregada por herramienta
  en `underpass-runtime/internal/app/recommender.go:55` a `underpass-runtime/internal/app/recommender.go:98`.
- El scoring online se define en `scoreTool()` y `applyTelemetryBoost()` en `underpass-runtime/internal/app/recommender.go:100` a `underpass-runtime/internal/app/recommender.go:195`.
- No aparecen lectores de politicas ni consumo del evento `tool_learning.policy.updated` en el runtime online. La busqueda en `internal/`, `cmd/workspace` y OpenAPI para `tool_policy`, `ReadPolicy`, `tool_learning.policy.updated`, `context_signature` y `thompson` no devuelve wiring online relevante; solo aparecen referencias en `services/tool-learning`, `specs/` y docs.
- La API de recomendaciones solo acepta `task_hint` y `top_k`, sin `context_signature`, en `underpass-runtime/api/openapi/workspace.v1.yaml:195` a `underpass-runtime/api/openapi/workspace.v1.yaml:220`.

#### Impacto para la demo

- Lo monitorizable hoy en la demo es un recomendador heuristico con ajuste por telemetria global por herramienta.
- No es correcto presentar el ranking mostrado por `RecommendTools` como "ranking Thompson" ni como "politica aprendida aplicada online".

### 2. Alta: ausencia de algoritmos SOTA en el path productivo del runtime

#### Evidencia

- La propia encuesta SOTA del repo describe el sistema actual como baseline `v1`:
  - `Algorithm: Thompson Sampling, Beta(alpha, beta) per tool`
  - `Context: None`
  - `Priors: Uniform Beta(1,1)`
  - `Limitations: No context, no tool metadata, no sequencing, no temporal adaptation`
  en `underpass-runtime/docs/research/SOTA_TOOL_SELECTION.md:16` a `underpass-runtime/docs/research/SOTA_TOOL_SELECTION.md:24`.
- El draft de investigacion marca como contribuciones y brazos experimentales precisamente lo que aun no esta en el path activo:
  - contextual bandits
  - seleccion jerarquica por familias
  - adaptacion no estacionaria
  - LLM priors
  - step-grained reward
  en `underpass-runtime/docs/research/PAPER_ADAPTIVE_TOOL_SELECTION.md:37` a `underpass-runtime/docs/research/PAPER_ADAPTIVE_TOOL_SELECTION.md:58` y `underpass-runtime/docs/research/PAPER_ADAPTIVE_TOOL_SELECTION.md:80` a `underpass-runtime/docs/research/PAPER_ADAPTIVE_TOOL_SELECTION.md:89`.
- El mismo roadmap de research deja como `NEXT`:
  - cablear `HyLinUCB` en `ComputePolicyUseCase`
  - cablear `LLM priors` en el CronJob
  - integrar `ContextDigest` en `TelemetryRecord`
  - añadir selector de algoritmo mas alla de TS
  - selector jerarquico por familias
  en `underpass-runtime/docs/research/README.md:46` a `underpass-runtime/docs/research/README.md:53`.
- `HyLinUCB` existe en el repo, pero su artefacto visible es un benchmark dedicado, no el scorer productivo del runtime:
  - implementacion en `underpass-runtime/services/tool-learning/internal/domain/hylinucb.go`
  - benchmark en `underpass-runtime/services/tool-learning/cmd/benchmark-hylinucb/main.go:1` a `underpass-runtime/services/tool-learning/cmd/benchmark-hylinucb/main.go:14`
- El binario batch de `tool-learning` solo expone hoy `--algorithm=thompson|thompson-llm` en `underpass-runtime/services/tool-learning/cmd/tool-learning/main.go:33` a `underpass-runtime/services/tool-learning/cmd/tool-learning/main.go:43`.
- En el runtime online, el path activo sigue siendo `static scoring + telemetry boost` en `underpass-runtime/internal/app/recommender.go:52` a `underpass-runtime/internal/app/recommender.go:129`.

#### Que falta exactamente para poder hablar de SOTA en runtime

Ausentes del path activo del producto:

- bandido contextual productivo tipo `HyLinUCB` o `Neural-LinUCB`
- seleccion jerarquica por familia de herramienta
- optimizacion de secuencias o tool chains
- reward step-grained o multi-dimensional en la politica activa
- recomendacion online refrescada por politicas contextuales aprendidas

Parcialmente presentes, pero no cerrados end-to-end:

- `Beta-SWTS` como query windowed en `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:116` a `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:145`
- `LLM priors` como sampler alternativo offline en `underpass-runtime/services/tool-learning/internal/domain/llm_priors.go:91` a `underpass-runtime/services/tool-learning/internal/domain/llm_priors.go:139`

#### Impacto para la demo

- Si el aprendizaje y descubrimiento de tools son un diferenciador clave, hoy conviene presentar el sistema como `v1 baseline + pipeline SOTA en construcción`, no como runtime ya SOTA.
- El documento deja base para exigir una hoja de ruta clara: primero contextualidad real y consumo de politicas, luego jerarquia y reward mas rico.
- En terminos de producto, este punto debe quedar marcado como **gap prioritario del runtime**:
  - degrada el valor diferencial de discovery/recommendation
  - limita la credibilidad tecnica de la demo
  - impide posicionar runtime como sistema de seleccion adaptativa avanzado

## Gap prioritario de producto

Si el objetivo es que `underpass-runtime` destaque por discovery y learning de
tools, el gap principal no es observabilidad ni UI. El gap principal es este:

- el runtime productivo todavia no ejecuta un recomendador SOTA

Mas concretamente:

- el path online no es contextual
- el path online no consume politicas aprendidas
- el path activo no explota jerarquia de familias
- el path activo no optimiza chains o secuencias
- el reward activo sigue siendo demasiado pobre para aprendizaje avanzado

Por tanto, para priorizacion de roadmap, este hallazgo debe leerse asi:

- `SOTA tool discovery and learning missing in active runtime path = core product gap`

### 3. Alta: la señal contextual necesaria para aprendizaje por contexto no esta cableada en la telemetria online

#### Evidencia

- `TelemetryRecord` declara campos ricos para aprendizaje:
  - `ToolsetID`
  - `RepoLanguage`
  - `ProjectType`
  - `Approved`
  en `underpass-runtime/internal/app/telemetry_types.go:11` a `underpass-runtime/internal/app/telemetry_types.go:29`.
- Los tests del recorder prueban que esos campos se persisten si llegan completos en `underpass-runtime/internal/adapters/telemetry/recorder_test.go:296` a `underpass-runtime/internal/adapters/telemetry/recorder_test.go:343`.
- Pero `recordTelemetry()` no rellena `ToolsetID`, `RepoLanguage`, `ProjectType` ni `Approved`; solo rellena campos basicos como `ToolName`, `RuntimeKind`, `TenantID`, `Status`, `DurationMs` y tamaños en `underpass-runtime/internal/app/service.go:1121` a `underpass-runtime/internal/app/service.go:1160`.
- `BuildContextDigest()` sabe derivar lenguaje, tipo de proyecto, frameworks y postura de seguridad en `underpass-runtime/internal/app/context_digest.go:34` a `underpass-runtime/internal/app/context_digest.go:64`, pero su uso real fuera de tests no aparece en el hot path online.
- El pipeline offline si esta modelado como contextual:
  - `context_signature` en la query DuckDB `GROUP BY context_signature, tool_id` en `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:101` a `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:145`
  - `ContextSignature` como clave de politica en `underpass-runtime/services/tool-learning/internal/domain/tool_policy.go:5` a `underpass-runtime/services/tool-learning/internal/domain/tool_policy.go:25`
  - formato `{task_family}:{lang}:{constraints_class}` en `underpass-runtime/services/tool-learning/internal/domain/context_signature.go:10` a `underpass-runtime/services/tool-learning/internal/domain/context_signature.go:20`

#### Impacto para la demo

- El runtime online no produce hoy una señal contextual equivalente a la que consume `tool-learning`.
- Si la demo quiere enseñar "aprendizaje por contexto", debe explicitar que esa parte vive en el pipeline offline, no en la respuesta online actual de recomendaciones.

### 4. Alta: no he encontrado el puente implementado entre la telemetria online de runtime y el lake Parquet que consume `tool-learning`

#### Evidencia

- El runtime online guarda telemetria en Valkey por herramienta:
  - backend `valkey` en `underpass-runtime/cmd/workspace/main.go:637` a `underpass-runtime/cmd/workspace/main.go:678`
  - `ValkeyRecorder.Record()` hace `RPush` a listas por herramienta en `underpass-runtime/internal/adapters/telemetry/recorder.go:63` a `underpass-runtime/internal/adapters/telemetry/recorder.go:73`
- El contrato compartido describe un componente intermedio distinto:
  - `Telemetry records -> Valkey | Workspace | Telemetry exporter (tool-learning)`
  - `Parquet -> MinIO lake | Telemetry exporter (tool-learning) | DuckDB aggregator (tool-learning)`
  en `underpass-runtime/specs/tool-policy/v1/contract.md:128` a `underpass-runtime/specs/tool-policy/v1/contract.md:135`
- `tool-learning` lee directamente del lake Parquet en S3/MinIO via DuckDB en `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:49` a `underpass-runtime/services/tool-learning/internal/adapters/duckdb/lake_reader.go:99`.
- El E2E del pipeline usa `seed-lake` sintetico para poblar el lake y luego correr `tool-learning`, no exporta datos del runtime online, en `underpass-runtime/e2e/tests/11-tool-learning-pipeline/main.go:75` a `underpass-runtime/e2e/tests/11-tool-learning-pipeline/main.go:114`.
- En el repo no aparece una implementacion clara de `telemetry exporter` o `telemetry-svc`; la busqueda devuelve referencias documentales en `specs/` y `docs/`, mas `seed-lake`, pero no un binario/product code equivalente.

#### Impacto para la demo

- No conviene vender "feedback loop automatico" desde invocacion runtime hasta politica aprendida salvo que ese exporter exista en otro repo o fuera de este checkout.
- Para monitorizacion honesta, hay que tratar `tool-learning` como pipeline batch independiente con sus propias entradas y salidas.

### 5. Media: las metricas de aprendizaje/recomendacion existen, pero varias no estan instrumentadas en el flujo real

#### Evidencia

- `KPIMetrics` define metricas de:
  - `workspace_success_on_first_tool`
  - `workspace_recommendation_acceptance_rate`
  - `workspace_policy_denial_rate_bad_recommendation`
  - `workspace_context_bytes_saved`
  en `underpass-runtime/internal/app/kpi_metrics.go:14` a `underpass-runtime/internal/app/kpi_metrics.go:43`.
- Su exposicion Prometheus esta implementada en `underpass-runtime/internal/app/kpi_metrics.go:132` a `underpass-runtime/internal/app/kpi_metrics.go:200`.
- Pero los call sites reales encontrados son solo:
  - `ObserveSessionCreated()` en `underpass-runtime/internal/app/service.go:168`
  - `ObserveSessionClosed()` en `underpass-runtime/internal/app/service.go:189`
  - `ObserveInvocationDenied()` en `underpass-runtime/internal/app/service.go:512`
  - `ObserveDiscoveryRequest()` en `underpass-runtime/internal/app/discovery.go:87`
- No aparecen usos reales de:
  - `ObserveToolCall`
  - `ObserveFirstToolResult`
  - `ObserveRecommendationUsed`
  - `ObservePolicyDenialAfterRecommendation`
  - `ObserveContextBytesSaved`
  fuera de tests.
- La doc de observabilidad las presenta como metricas del learning loop en `underpass-runtime/docs/observability.md:75` a `underpass-runtime/docs/observability.md:87`.

#### Impacto para la demo

- Hoy el dashboard de la demo no deberia prometer acceptance rate de recomendaciones, first-tool success o bad recommendation denial rate como metricas vivas fiables si salen solo de `/metrics`.
- Las metricas fiables hoy son principalmente:
  - `invocations_total`
  - `denied_total`
  - `duration_ms`
  - sesiones creadas/cerradas
  - discovery requests
  - invocaciones denegadas por reason

### 6. Media: la retencion y la frescura de la telemetria online no coinciden con lo documentado

#### Evidencia

- El recorder guarda un `ttl` con default de 7 dias en `underpass-runtime/internal/adapters/telemetry/recorder.go:27` a `underpass-runtime/internal/adapters/telemetry/recorder.go:45`.
- La documentacion tambien habla de TTL de 7 dias para `valkey` en `underpass-runtime/docs/observability.md:132` a `underpass-runtime/docs/observability.md:143`.
- Pero `Record()` solo hace `RPush`; no aplica expiracion ni `EXPIRE` a la lista en `underpass-runtime/internal/adapters/telemetry/recorder.go:63` a `underpass-runtime/internal/adapters/telemetry/recorder.go:73`.
- El agregador online lee la lista completa con `LRange 0 -1` y calcula stats sobre todos los registros disponibles en:
  - `underpass-runtime/internal/adapters/telemetry/recorder.go:76` a `underpass-runtime/internal/adapters/telemetry/recorder.go:93`
  - `underpass-runtime/internal/adapters/telemetry/aggregator.go:120` a `underpass-runtime/internal/adapters/telemetry/aggregator.go:149`
  - `underpass-runtime/internal/adapters/telemetry/aggregator.go:161` a `underpass-runtime/internal/adapters/telemetry/aggregator.go:198`

#### Impacto para la demo

- El boost online por telemetria no tiene hoy una semantica clara de recencia.
- En demos largas, los stats por herramienta pueden acumular historia vieja indefinidamente y sesgar el ranking.

### 7. Media: el pipeline `tool-learning` es observable, pero no es metrics-first

#### Evidencia

- El binario `tool-learning` es un CLI batch que:
  - parsea flags
  - construye adapters
  - ejecuta el caso de uso
  - loggea resultado
  en `underpass-runtime/services/tool-learning/cmd/tool-learning/main.go:25` a `underpass-runtime/services/tool-learning/cmd/tool-learning/main.go:140`
- No aparece servidor HTTP ni endpoint `/metrics` en ese binario.
- El chart despliega `CronJob`, no `Deployment` ni `Service`, en `underpass-runtime/charts/underpass-runtime/templates/cronjob-tool-learning.yaml:1` a `underpass-runtime/charts/underpass-runtime/templates/cronjob-tool-learning.yaml:211`.
- La observabilidad documentada para esa parte se apoya en alertas de `CronJob failed/missed`, no en metricas de politica expuestas por servicio, en `underpass-runtime/docs/observability.md:146` a `underpass-runtime/docs/observability.md:159`.

#### Impacto para la demo

- La demo puede monitorizar `tool-learning`, pero la fuente debe ser:
  - estado del CronJob/Job
  - logs con `aggregates_read`, `policies_written`, `policies_filtered`, `duration_ms`
  - evento NATS `tool_learning.policy.updated`
  - recuento/frescura de claves `tool_policy:*`
  - snapshots de auditoria en S3
- No existe hoy un `/metrics` de `tool-learning` que simplifique ese panel.

### 8. Baja: hay deriva documental entre contratos y estado real

#### Evidencia

- El ADR dice:
  - `tool.policy.updated`
  - clave `tool_policy:<tool_name>`
  en `underpass-runtime/docs/adr/ADR-003-thompson-sampling-tool-recommendations.md:48` a `underpass-runtime/docs/adr/ADR-003-thompson-sampling-tool-recommendations.md:66`
- El contrato y la implementacion actual usan:
  - `tool_learning.policy.updated`
  - `tool_policy:{context_signature}:{tool_id}`
  en:
  - `underpass-runtime/specs/tool-policy/v1/contract.md:7` a `underpass-runtime/specs/tool-policy/v1/contract.md:18`
  - `underpass-runtime/specs/tool-policy/v1/contract.md:60` a `underpass-runtime/specs/tool-policy/v1/contract.md:75`
  - `underpass-runtime/services/tool-learning/internal/adapters/nats/publisher.go:15` a `underpass-runtime/services/tool-learning/internal/adapters/nats/publisher.go:67`
  - `underpass-runtime/services/tool-learning/internal/adapters/valkey/policy_store.go:45` a `underpass-runtime/services/tool-learning/internal/adapters/valkey/policy_store.go:85`
- El README del runtime sigue presentando Thompson Sampling como mecanismo de seleccion online en `underpass-runtime/README.md:16` a `underpass-runtime/README.md:20`, pero eso no coincide con `internal/app/recommender.go`.

#### Impacto para la demo

- Si se nombran paneles, subjects o claves segun ADR viejo, la demo acabara monitorizando nombres incorrectos o engañosos.

## Lo que si merece la pena monitorizar en la demo

### Runtime online

Fuentes utilizables hoy:

- `GET /v1/sessions/{id}/tools/recommendations`
- `GET /v1/sessions/{id}/tools/discovery?detail=full`
- `GET /metrics`

Señales recomendadas:

- top-k recomendado actual
- score y razon textual (`why`) de cada recomendacion
- stats por herramienta en discovery full:
  - `success_rate`
  - `p50_duration_ms`
  - `p95_duration_ms`
  - `deny_rate`
  - `invocation_count`
- invocations/failures/denials/latency desde Prometheus

Lo que deberia etiquetarse como "heuristico" o "online telemetry":

- ranking de recomendaciones
- penalizaciones por riesgo, approval, coste, side effects
- boost por exito historico global

### Tool-learning offline

Fuentes utilizables hoy:

- `CronJob` y `Job` de Helm
- logs de `tool-learning`
- evento NATS `tool_learning.policy.updated`
- claves Valkey `tool_policy:*`
- snapshots de auditoria en S3

Señales recomendadas:

- ultimo job horario/diario: `succeeded` o `failed`
- timestamp del ultimo job exitoso
- `aggregates_read`
- `policies_written`
- `policies_filtered`
- `duration_ms`
- numero de claves `tool_policy:*`
- frescura maxima/minima de `freshness_ts`
- sample policies por contexto y herramienta

## Lo que no deberia afirmarse todavia en la demo

- que el runtime online ya aplica Thompson Sampling en `RecommendTools`
- que las recomendaciones online ya se recalculan por `context_signature`
- que el evento `tool_learning.policy.updated` refresca el ranking online en runtime
- que el loop runtime -> telemetry -> lake -> policy -> runtime este cerrado dentro de este repo
- que el runtime productivo ya opera con algoritmos SOTA de tool selection

## Recomendacion de producto para la demo

Presentacion honesta y cohesionada:

1. Panel `Runtime recommendation`
   - "heuristic + live telemetry"
   - mostrar top-k, why, risk, deny rate, p95

2. Panel `Tool learning`
   - "offline policy computation"
   - mostrar jobs, politicas escritas, freshness, sample `alpha/beta/confidence`

3. Badge de relacion entre ambos
   - `future online policy consumption`
   - o `policy pipeline ready, online wiring pending`

Asi la demo mantiene cohesion con la arquitectura objetivo sin ocultar el estado real del software.

## Cambios minimos que cerrarian la brecha

Si el objetivo es monitorizar un loop completo de aprendizaje, el runtime deberia incorporar al menos:

1. un `PolicyReader` online para `tool_policy:{context_signature}:{tool_id}`
2. construccion real de `context_signature` desde sesion, tarea y/o `ContextDigest`
3. consumo de `tool_learning.policy.updated` para invalidar cache o refrescar ranking
4. relleno real de `RepoLanguage`, `ProjectType`, `Approved`, `ToolsetID` y `context_signature` en telemetria
5. exportador real desde telemetria runtime hacia el lake Parquet, si va a seguir existiendo esa separacion
6. metricas propias del pipeline de politicas si se quiere un panel Prometheus/Grafana limpio

## Resumen ejecutivo

Estado real:

- `tool-learning` offline: maduro para demo tecnica
- telemetria runtime: util para observabilidad operacional
- recomendacion online del runtime: funcional, pero todavia heuristica
- loop de aprendizaje cerrado runtime->policy->runtime: no demostrado en esta implementacion

La demo puede monitorizarlo bien si separa claramente:

- decision online actual
- aprendizaje offline actual

y evita venderlos como la misma pieza.
