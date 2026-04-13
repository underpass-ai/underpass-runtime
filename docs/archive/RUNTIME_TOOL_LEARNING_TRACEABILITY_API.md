# API a exponer para tool learning, trazabilidad y auditabilidad en `underpass-runtime`

Fecha: 2026-04-02

## Posicion arquitectonica

Si `tool discovery` y `tool learning` son diferenciadores de producto, su
evidencia no puede depender de logs ad hoc, caches opacas ni explicaciones
manuales.

La postura correcta es esta:

- el sistema sigue siendo `event-driven`
- el bus es la verdad primaria de los hechos de dominio
- la API es el plano de consulta, reconstruccion y auditoria sobre esos hechos
- la demo debe consumir esa misma superficie, no un camino especial

Este documento fija la superficie de producto necesaria para demostrar de forma
inequivoca:

- que el runtime descubrio ciertas tools
- que recomendo unas y no otras
- que algoritmo tomo la decision
- que politica o agregados sostuvieron esa decision
- que run produjo esa politica
- que cadena de eventos prueba toda la causalidad

## Estado del contrato

La primera bajada de esta propuesta a artefactos versionados queda en:

- `specs/underpass/runtime/learning/v1/learning.proto`
- `specs/underpass/runtime/learning/v1/events.proto`
- `specs/underpass/runtime/learning/v1/contract.md`
- `specs/underpass/runtime/learning/v1/events.md`
- `api/openapi/learning.v1.yaml`
- `api/asyncapi/learning-events.v1.yaml`

## Evidencia actual

### Contratos sincronos ya expuestos

El runtime expone hoy:

- `SessionService`
- `CapabilityCatalogService`
- `InvocationService`
- `HealthService`

en `underpass-runtime/specs/underpass/runtime/v1/runtime.proto:17` a
`underpass-runtime/specs/underpass/runtime/v1/runtime.proto:56`.

En particular:

- `DiscoverTools` existe en `runtime.proto:26` a `runtime.proto:28`
- `RecommendTools` existe en `runtime.proto:30` a `runtime.proto:31`
- `RecommendToolsRequest` y `RecommendToolsResponse` estan en
  `runtime.proto:289` a `runtime.proto:295` y `runtime.proto:362` a
  `runtime.proto:372`

Esto es suficiente para uso operativo basico, pero no para trazabilidad fuerte.

### Contratos async ya expuestos

El runtime ya es `event-driven` y publica eventos de dominio via NATS
JetStream.

Evidencia:

- contrato async publico en
  `underpass-runtime/specs/underpass/runtime/v1/events.proto`
- documentacion del contrato async en
  `underpass-runtime/specs/underpass/runtime/v1/events.md`
- emision desde runtime en
  `underpass-runtime/internal/app/service.go:1163`
- `outbox` con semantica at-least-once en
  `underpass-runtime/internal/adapters/eventbus/outbox.go:24`

Hoy el catalogo publico cubre sobre todo:

- `workspace.session.created`
- `workspace.session.closed`
- `workspace.invocation.started`
- `workspace.invocation.completed`
- `workspace.invocation.denied`

### Pipeline offline de learning ya existente

`tool-learning` ya tiene piezas persistentes y eventos:

- lectura de agregados desde lake Parquet
- escritura de politicas en Valkey
- snapshots en S3
- publicacion de `tool_learning.policy.updated`

Evidencia:

- `underpass-runtime/services/tool-learning/internal/app/ports.go:10` a
  `underpass-runtime/services/tool-learning/internal/app/ports.go:35`
- `underpass-runtime/services/tool-learning/internal/adapters/valkey/policy_store.go:58`
  a `policy_store.go:85`
- `underpass-runtime/services/tool-learning/internal/adapters/s3/audit_store.go`
- `underpass-runtime/services/tool-learning/internal/adapters/nats/publisher.go:15`
  a `publisher.go:67`

## Diagnostico

La superficie actual sirve para operar el runtime, pero no para demostrar de
forma fuerte el aprendizaje y descubrimiento de tools.

Faltan contratos para:

- identificar cada discovery y cada recomendacion como objetos persistidos
- separar heuristica, telemetria y politica aprendida
- recuperar la politica exacta usada en la decision
- recuperar el run de `tool-learning` que produjo esa politica
- recuperar el snapshot de auditoria y su checksum
- recuperar la cadena causal de eventos
- mostrar algoritmo, version y parametros de forma estable
- reconstruir explicaciones en algoritmos estocasticos o contextuales

Tambien falta una verdad clara sobre ownership:

- NATS y los eventos son el source of truth de hechos
- Valkey y S3 son stores de materializacion y auditoria
- la API debe consultar y reconstruir sobre esos hechos

Si esto no se modela asi, la arquitectura se degrada a polling sobre caches.

## Conclusión de diseño

Para trazabilidad y auditabilidad reales, no basta con ampliar
`RecommendTools`.

Hace falta separar tres superficies:

1. API agente
2. API read-only de evidencia y auditoria
3. señales operativas

### 1. API agente

Sirve para discovery, recommendations e invocacion.

Debe ser:

- rapida
- estable
- simple
- orientada a ejecucion

### 2. API de evidencia y auditoria

Sirve para:

- demo
- operaciones
- auditoria
- debugging
- investigacion de decisiones

Debe ser:

- persistente
- trazable
- versionada
- reproducible
- segura

### 3. Señales operativas

Sirven para dashboards y alertas:

- Prometheus
- NATS
- logs estructurados

No sustituyen a la API de evidencia.

## Recomendacion arquitectonica

### Recomendacion principal

No convertir el `CronJob` de `tool-learning` en servidor API.

El batch debe seguir siendo batch.

La API de evidencia debe exponerse como un bounded context read-only separado,
idealmente:

- `LearningEvidenceService`

y desplegarse inicialmente dentro del binario largo de `underpass-runtime`.

### Recomendacion principal adicional

No tratar la evidencia como una tabla mutable sin historia.

La forma correcta es:

1. emitir hechos canonicos al bus
2. persistirlos de forma durable o relayarlos via `outbox`
3. construir materializaciones y snapshots
4. exponerlos via API read-only

La API no debe inventarse su propia verdad paralela a NATS.

### Por qué

- `underpass-runtime` ya es proceso persistente, autenticado y expuesto por gRPC/HTTP
- el `CronJob` no es una base correcta para consultas interactivas
- la evidencia mezcla datos online y offline
- demo, operaciones y auditoria necesitan una vista consistente
- el runtime ya publica eventos y ya soporta `outbox`

## Principios del contrato

### Event-first

El bus es primario y la API es derivada.

Para discovery, recommendation y learning:

- los hechos importantes deben existir como eventos
- la API debe leer de read models, snapshots o bundles derivados de eventos
- ninguna cache debe ser la unica prueba de que algo ocurrio

### Causalidad obligatoria

Toda decision, politica o run expuesto debe incluir:

- `event_id`
- `event_type`
- `event_subject`
- `correlation_id`
- `causation_id`
- `trace_id`
- `span_id`
- `produced_at`

Sin eso no hay trazabilidad fuerte en una arquitectura event-driven.

### Semantica explicita

- emision: at-least-once
- consumo: idempotente
- consultas: eventualmente consistentes
- snapshots: versionados e inmutables

La API y la demo deben explicitar esta semantica. No deben vender consistencia
lineal donde no la hay.

### Identidad estable

Toda decision o politica expuesta debe:

1. tener identificador estable
2. incluir algoritmo y version
3. incluir parametros relevantes
4. incluir referencia a dataset o ventana de datos
5. incluir referencias a snapshot o artefacto de auditoria
6. declarar su source of truth
7. incluir referencias a eventos y causalidad
8. poder redactorse por rol sin romper trazabilidad

## Source of truth que debe declarar cada recomendacion

Cada recomendacion debe declarar de forma explicita:

- `decision_source`
- `algorithm_id`
- `algorithm_version`
- `policy_mode`
- `event_mode`

Valores ejemplo para `decision_source`:

- `HEURISTIC_ONLY`
- `HEURISTIC_WITH_TELEMETRY`
- `LEARNED_POLICY_TS`
- `LEARNED_POLICY_SWTS`
- `LEARNED_POLICY_TSLLM`
- `CONTEXTUAL_BANDIT_HYLINUCB`
- `HYBRID`

Valores ejemplo para `event_mode`:

- `SYNC_RESPONSE_WITH_EVENT_FACT`
- `ASYNC_DECISION_FACT`
- `REPLAYED_FROM_EVENT_LOG`

Sin esto, la demo no puede probar qué subsistema actuó realmente.

## Recursos que deben existir

### EventFact

Objeto normalizado para exponer hechos canonicos sin obligar al cliente final a
leer NATS directamente.

Campos minimos:

- `event_id`
- `subject`
- `type`
- `version`
- `timestamp`
- `tenant_id`
- `actor_id`
- `session_id`
- `correlation_id`
- `causation_id`
- `payload`

### RecommendationDecision

Objeto persistido por cada respuesta de recomendacion.

Campos minimos:

- `recommendation_id`
- `session_id`
- `tenant_id`
- `actor_id`
- `task_hint`
- `top_k`
- `context_signature`
- `decision_source`
- `algorithm_id`
- `algorithm_version`
- `feature_schema_version`
- `policy_snapshot_ref`
- `policy_run_id`
- `candidate_count`
- `recommendations[]`
- `created_at`
- `event_id`
- `event_subject`
- `event_version`
- `correlation_id`
- `causation_id`
- `trace_id`
- `span_id`

Cada item de `recommendations[]` debe incluir:

- `tool_id`
- `rank`
- `final_score`
- `score_breakdown`
- `why`
- `estimated_cost`
- `policy_notes`
- `policy_ref`
- `telemetry_ref`

### DiscoverySnapshot

Objeto persistido por cada discovery relevante.

Campos minimos:

- `discovery_id`
- `session_id`
- `detail`
- `filters`
- `total_tools`
- `filtered_tools`
- `tools[]`
- `created_at`
- `event_id`
- `event_subject`
- `correlation_id`

### ToolPolicy

Politica computada para `(context_signature, tool_id)`.

Campos minimos:

- `policy_id`
- `context_signature`
- `tool_id`
- `algorithm_id`
- `algorithm_version`
- `alpha`
- `beta`
- `confidence`
- `p95_latency_ms`
- `p95_cost`
- `error_rate`
- `n_samples`
- `freshness_ts`
- `run_id`
- `snapshot_ref`
- `event_id`
- `event_subject`

### PolicyRun

Representa una ejecucion de `tool-learning`.

Campos minimos:

- `run_id`
- `schedule`
- `status`
- `algorithm_id`
- `algorithm_version`
- `feature_schema_version`
- `window`
- `constraints`
- `started_at`
- `completed_at`
- `aggregates_read`
- `policies_written`
- `policies_filtered`
- `duration_ms`
- `lake_ref`
- `snapshot_ref`
- `event_ref`
- `input_event_refs[]`
- `output_event_refs[]`

### TelemetryAggregate

Agregado auditable sobre el que se apoyan politicas o explicaciones.

Campos minimos:

- `aggregate_id`
- `tool_id`
- `context_signature`
- `window`
- `total`
- `successes`
- `failures`
- `deny_count`
- `success_rate`
- `deny_rate`
- `p50_duration_ms`
- `p95_duration_ms`
- `p95_cost`
- `source_ref`
- `computed_at`

### EvidenceBundle

Objeto empaquetado para demo, debugging o auditoria.

Debe unir:

- `RecommendationDecision`
- `ToolPolicy`
- `PolicyRun`
- `TelemetryAggregate`
- `snapshot metadata`
- `event lineage`

## Eventos que deben existir

La trazabilidad fuerte exige ampliar el catalogo async para learning.

### Runtime

- `runtime.learning.discovery.recorded`
- `runtime.learning.recommendation.emitted`
- `runtime.learning.recommendation.accepted`
- `runtime.learning.recommendation.rejected`

### Tool learning

- `tool_learning.run.started`
- `tool_learning.run.completed`
- `tool_learning.run.failed`
- `tool_learning.policy.computed`
- `tool_learning.policy.updated`
- `tool_learning.policy.degraded`
- `tool_learning.snapshot.published`

### Relacion con eventos existentes

Los eventos existentes:

- `workspace.session.created`
- `workspace.session.closed`
- `workspace.invocation.started`
- `workspace.invocation.completed`
- `workspace.invocation.denied`

deben poder enlazarse con discovery, recommendation y policy por
`correlation_id` y `causation_id`.

Esa cadena causal es la base real de la auditoria.

## Endpoints recomendados

## A. Agente

Mantener los endpoints actuales, pero añadir versiones estructuradas y
persistentes.

### Mantener por compatibilidad

- `GET /v1/sessions/{session_id}/tools/discovery`
- `GET /v1/sessions/{session_id}/tools/recommendations`

### Añadir

- `POST /v1/sessions/{session_id}/tools/discovery:query`
- `POST /v1/sessions/{session_id}/tools/recommendations:query`

Motivo:

- filtros y contexto van a crecer
- la recomendacion necesita `body` estructurado
- la respuesta debe devolver `recommendation_id` o `discovery_id`
- la respuesta debe devolver tambien referencias al hecho emitido al bus

Respuesta minima de `recommendations:query`:

```json
{
  "recommendation_id": "rec_01J...",
  "event_id": "evt_01J...",
  "event_subject": "runtime.learning.recommendation.emitted",
  "decision_source": "HEURISTIC_WITH_TELEMETRY",
  "algorithm_id": "runtime-heuristic-v1",
  "algorithm_version": "2026-04-02",
  "task_hint": "patch payment timeout handling",
  "top_k": 5,
  "recommendations": [
    {
      "tool_id": "fs.read",
      "rank": 1,
      "final_score": 1.15,
      "why": "low risk, no side effects, low cost, matches task hint"
    }
  ]
}
```

## B. Evidencia y auditoria

Esta es la superficie nueva importante.

### Service recomendado

- `LearningEvidenceService`

### Endpoints HTTP recomendados

- `GET /v1/learning/status`
- `GET /v1/learning/events`
- `GET /v1/learning/events/{event_id}`
- `GET /v1/learning/recommendations/{recommendation_id}`
- `GET /v1/learning/recommendations/{recommendation_id}/events`
- `GET /v1/learning/discovery/{discovery_id}`
- `GET /v1/learning/discovery/{discovery_id}/events`
- `GET /v1/learning/policies`
- `GET /v1/learning/policies/{context_signature}/{tool_id}`
- `GET /v1/learning/policies/{context_signature}/{tool_id}/lineage`
- `GET /v1/learning/runs`
- `GET /v1/learning/runs/{run_id}`
- `GET /v1/learning/runs/{run_id}/events`
- `GET /v1/learning/runs/{run_id}/snapshot`
- `GET /v1/learning/aggregates`
- `GET /v1/learning/aggregates/{context_signature}/{tool_id}`
- `GET /v1/learning/evidence/recommendations/{recommendation_id}`

Filtros minimos para `GET /v1/learning/events`:

- `subject_prefix`
- `type`
- `session_id`
- `correlation_id`
- `causation_id`
- `from_ts`
- `to_ts`
- `page_size`
- `page_token`

### Metodos gRPC recomendados

```proto
service LearningEvidenceService {
  rpc GetLearningStatus(GetLearningStatusRequest) returns (GetLearningStatusResponse);
  rpc GetEventFact(GetEventFactRequest) returns (EventFact);
  rpc ListEventFacts(ListEventFactsRequest) returns (ListEventFactsResponse);
  rpc GetRecommendationDecision(GetRecommendationDecisionRequest) returns (RecommendationDecision);
  rpc ListRecommendationEvents(ListRecommendationEventsRequest) returns (ListEventFactsResponse);
  rpc GetDiscoverySnapshot(GetDiscoverySnapshotRequest) returns (DiscoverySnapshot);
  rpc ListDiscoveryEvents(ListDiscoveryEventsRequest) returns (ListEventFactsResponse);
  rpc ListPolicies(ListPoliciesRequest) returns (ListPoliciesResponse);
  rpc GetPolicy(GetPolicyRequest) returns (ToolPolicyEvidence);
  rpc ListPolicyRuns(ListPolicyRunsRequest) returns (ListPolicyRunsResponse);
  rpc GetPolicyRun(GetPolicyRunRequest) returns (PolicyRunEvidence);
  rpc ListPolicyRunEvents(ListPolicyRunEventsRequest) returns (ListEventFactsResponse);
  rpc GetAggregate(GetAggregateRequest) returns (TelemetryAggregateEvidence);
  rpc GetEvidenceBundle(GetEvidenceBundleRequest) returns (EvidenceBundle);
}
```

## C. Señales operativas

### Prometheus

Anadir metricas especificas de learning:

- `workspace_recommendations_total{decision_source,algorithm_id}`
- `workspace_recommendation_candidates_total{algorithm_id}`
- `workspace_learning_policy_freshness_seconds{context_signature,tool}`
- `workspace_learning_policy_runs_total{schedule,status,algorithm_id}`
- `workspace_learning_policy_duration_ms`
- `workspace_learning_policies_written_total`
- `workspace_learning_policies_filtered_total`
- `workspace_learning_aggregate_staleness_seconds`
- `workspace_learning_evidence_requests_total{resource}`

### NATS

Mantener:

- `tool_learning.policy.updated`

Anadir:

- `runtime.learning.discovery.recorded`
- `runtime.learning.recommendation.emitted`
- `runtime.learning.recommendation.accepted`
- `runtime.learning.recommendation.rejected`
- `tool_learning.run.started`
- `tool_learning.run.completed`
- `tool_learning.run.failed`
- `tool_learning.policy.computed`
- `tool_learning.policy.degraded`
- `tool_learning.snapshot.published`

La API debe poder reconstruir evidencia a partir de estos hechos, sin exigir al
cliente final una suscripcion directa a NATS.

## Qué debe registrar la evidencia segun algoritmo

### Heuristico

Minimo:

- score base
- penalties por riesgo, approval, side effects y cost
- bonus por hint matching
- stats usadas para boost de telemetria

### Thompson / SWTS

Minimo:

- `alpha`
- `beta`
- `confidence`
- `sampled_score`
- `window`
- `freshness_ts`
- `policy_run_id`

Si se quiere reproducibilidad fuerte:

- `rng_seed_ref` o `sampling_trace_ref`

### HyLinUCB

Minimo:

- `context_features`
- `arm_features`
- `alpha_exploration`
- `shared_model_version`
- `arm_model_version`
- `predicted_reward`
- `uncertainty_term`
- `final_ucb_score`

### LLM priors

Minimo:

- `prior_source_model`
- `prior_prompt_version`
- `equivalent_n`
- `estimated_p`
- `prior_alpha`
- `prior_beta`
- `rationale_ref`

## Requisitos de auditabilidad fuertes

Para que una auditoria sea inequivoca, una recomendacion debe permitir
responder:

1. Que pidio el cliente
2. Que contexto se uso
3. Que algoritmo tomo la decision
4. Que version exacta del algoritmo estaba activa
5. Que candidatos existian
6. Por que el top-k final quedo asi
7. Que politica o agregados sostuvieron la decision
8. Que run produjo esa politica
9. Que snapshot y que checksum respaldan ese run
10. Que eventos prueban la cadena causal completa

Si cualquiera de esos puntos no puede responderse por API, no hay trazabilidad
fuerte.

## Seguridad y redaccion

La API debe separar claramente dos perfiles:

- `agent/client`
- `operator/auditor`

### Agent/client

Puede ver:

- recomendaciones
- reasons resumidas
- metadata de herramienta

No necesita ver:

- snapshot URIs
- lake refs
- raw feature payloads
- semillas o trazas de RNG

### Operator/auditor

Puede ver:

- lineage completo
- refs a S3
- checksums
- parametros del algoritmo
- agregados
- source window
- `event_id`, `subject`, `correlation_id` y `causation_id`

### Redaccion minima

- pseudonimizar IDs sensibles en datos de aprendizaje
- no exponer paths privados o secretos del workspace
- separar `evidence_ref` de `evidence_content`

## Versionado

Esta API debe nacer versionada desde el principio.

Recomendacion:

- `underpass.runtime.learning.v1`

No mezclarla con el contrato actual de session, catalog e invocation hasta que
madure.

## MVP recomendado

Si hay que priorizar, el primer slice serio deberia hacer esto:

1. mantener `DiscoverTools` y `RecommendTools`
2. hacer que `RecommendTools` persista `RecommendationDecision`
3. emitir `runtime.learning.recommendation.emitted`
4. exponer `GetRecommendationDecision`
5. exponer `ListRecommendationEvents`
6. exponer `GetPolicy`
7. exponer `GetPolicyRun`
8. exponer `GetEvidenceBundle`
9. emitir `tool_learning.run.completed` y `tool_learning.policy.computed`

Con eso ya tendriamos:

- demo convincente
- debugging serio
- evidencia de algoritmo y version
- enlace a policy run
- causalidad event-driven explicable
- base para auditoria

## Fase siguiente

La siguiente fase deberia anadir:

- `context_signature` real en runtime online
- consumo online de politicas aprendidas
- event sourcing de `RecommendationDecision`
- policy lineage completo
- metricas de freshness y source of truth
- reconstruccion de evidence bundles por `event_id`

## Posicion final

Si `tool learning` y `tool discovery` van a ser diferenciadores de producto, la
API que falta no es un accesorio de demo.

Es parte del propio producto.

Sin una API de evidencia derivada de eventos:

- no puedes demostrar que el sistema aprende
- no puedes auditar por que recomendo una tool
- no puedes separar heuristica de politica aprendida
- no puedes sostener una narrativa SOTA con pruebas verificables

La recomendacion tecnica y de producto es:

- mantener la API agente ligera
- crear una API read-only de evidencia y aprendizaje
- tratar `EventFact`, `RecommendationDecision`, `ToolPolicy`, `PolicyRun` y
  `EvidenceBundle` como recursos de primer nivel

Solo con eso el tool learning deja de ser una afirmacion y pasa a ser una
capacidad demostrable, trazable y auditable dentro de una arquitectura
event-driven.
