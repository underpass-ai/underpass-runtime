// E2E test: Tool-Learning Pipeline
//
// Validates the full pipeline: seed data → compute policies → verify outputs.
// Steps:
//  1. Seed synthetic telemetry to MinIO (via seed-lake binary)
//  2. Run tool-learning --schedule=hourly
//  3. Verify policies written to Valkey
//  4. Verify audit snapshot written to MinIO
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"

	"github.com/underpass-ai/underpass-runtime/services/tool-learning/internal/domain"
)

type stepResult struct {
	Step   string         `json:"step"`
	Status string         `json:"status"`
	Data   map[string]any `json:"data,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type evidence struct {
	TestID    string       `json:"test_id"`
	RunID     string       `json:"run_id"`
	Status    string       `json:"status"`
	StartedAt string      `json:"started_at"`
	EndedAt   string       `json:"ended_at"`
	Steps     []stepResult `json:"steps"`
}

func main() {
	os.Exit(run())
}

func run() int {
	ev := evidence{
		TestID:    "11-tool-learning-pipeline",
		RunID:     fmt.Sprintf("e2e-tl-pipeline-%d", time.Now().Unix()),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Status:    "failed",
	}
	defer func() {
		ev.EndedAt = time.Now().UTC().Format(time.RFC3339)
		emitEvidence(ev)
	}()

	endpoint := envOr("S3_ENDPOINT", "minio:9000")
	accessKey := envOr("S3_ACCESS_KEY", "")
	secretKey := envOr("S3_SECRET_KEY", "")
	useSSL := envOr("S3_USE_SSL", "false") == "true"
	_ = envOr("LAKE_BUCKET", "telemetry-lake") // used by seed-lake via env
	auditBucket := envOr("AUDIT_BUCKET", "policy-audit")
	valkeyAddr := envOr("VALKEY_ADDR", "valkey:6379")
	valkeyPass := os.Getenv("VALKEY_PASSWORD")
	valkeyPrefix := envOr("VALKEY_KEY_PREFIX", "e2e_tl_11")
	seedHours := envOr("SEED_HOURS", "2")
	seedPerHour := envOr("SEED_PER_HOUR", "50")

	// Seed credentials: use SEED_S3_* if set (root/admin), fallback to S3_*
	seedAccessKey := envOr("SEED_S3_ACCESS_KEY", accessKey)
	seedSecretKey := envOr("SEED_S3_SECRET_KEY", secretKey)

	// Step 1: Seed telemetry data
	fmt.Println("=== Step 1: Seed telemetry data ===")
	seedCmd := exec.Command("/app/seed-lake",
		"--hours="+seedHours,
		"--invocations-per-hour="+seedPerHour,
	)
	seedCmd.Stdout = os.Stdout
	seedCmd.Stderr = os.Stderr
	// Override S3 credentials for seeding (needs write access to telemetry-lake)
	seedCmd.Env = replaceEnv(os.Environ(), map[string]string{
		"S3_ACCESS_KEY": seedAccessKey,
		"S3_SECRET_KEY": seedSecretKey,
	})

	if err := seedCmd.Run(); err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "seed", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL seed: %v\n", err)
		return 1
	}
	ev.Steps = append(ev.Steps, stepResult{Step: "seed", Status: "passed", Data: map[string]any{
		"hours": seedHours, "per_hour": seedPerHour,
	}})
	fmt.Println("PASS seed")

	// Step 2: Run tool-learning --schedule=hourly
	fmt.Println("=== Step 2: Compute policies ===")
	computeCmd := exec.Command("/app/tool-learning", "--schedule=hourly")
	computeCmd.Stdout = os.Stdout
	computeCmd.Stderr = os.Stderr
	env := os.Environ()
	env = append(env, "VALKEY_KEY_PREFIX="+valkeyPrefix)
	computeCmd.Env = env

	if err := computeCmd.Run(); err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "compute", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL compute: %v\n", err)
		return 1
	}
	ev.Steps = append(ev.Steps, stepResult{Step: "compute", Status: "passed"})
	fmt.Println("PASS compute")

	// Step 3: Verify Valkey
	fmt.Println("=== Step 3: Verify Valkey policies ===")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{
		Addr:     valkeyAddr,
		Password: valkeyPass,
	})
	defer func() { _ = rdb.Close() }()

	var allKeys []string
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, valkeyPrefix+":*", 100).Result()
		if err != nil {
			ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "failed", Error: err.Error()})
			fmt.Fprintf(os.Stderr, "FAIL verify_valkey: scan: %v\n", err)
			return 1
		}
		allKeys = append(allKeys, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	if len(allKeys) == 0 {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "failed", Error: "no policy keys found"})
		fmt.Fprintln(os.Stderr, "FAIL verify_valkey: no policy keys found")
		return 1
	}

	// Validate one policy
	raw, err := rdb.Get(ctx, allKeys[0]).Result()
	if err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL verify_valkey: get: %v\n", err)
		return 1
	}
	var policy domain.ToolPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL verify_valkey: unmarshal: %v\n", err)
		return 1
	}
	if policy.ToolID == "" || policy.Alpha <= 0 || policy.NSamples <= 0 {
		msg := fmt.Sprintf("invalid policy: tool=%q alpha=%.2f n=%d", policy.ToolID, policy.Alpha, policy.NSamples)
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "failed", Error: msg})
		fmt.Fprintf(os.Stderr, "FAIL verify_valkey: %s\n", msg)
		return 1
	}

	ev.Steps = append(ev.Steps, stepResult{Step: "verify_valkey", Status: "passed", Data: map[string]any{
		"keys_found":  len(allKeys),
		"sample_tool": policy.ToolID,
		"sample_ctx":  policy.ContextSignature,
		"alpha":       policy.Alpha,
		"n_samples":   policy.NSamples,
	}})
	fmt.Printf("PASS verify_valkey: %d keys, sample=%s\n", len(allKeys), policy.ToolID)

	// Cleanup E2E keys from Valkey
	if len(allKeys) > 0 {
		_ = rdb.Del(ctx, allKeys...).Err()
	}

	// Step 4: Verify MinIO audit snapshot
	fmt.Println("=== Step 4: Verify S3 audit snapshot ===")
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL verify_s3: client: %v\n", err)
		return 1
	}

	var auditObjects []string
	for obj := range mc.ListObjects(ctx, auditBucket, minio.ListObjectsOptions{Prefix: "audit/", Recursive: true}) {
		if obj.Err != nil {
			ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: obj.Err.Error()})
			fmt.Fprintf(os.Stderr, "FAIL verify_s3: list: %v\n", obj.Err)
			return 1
		}
		auditObjects = append(auditObjects, obj.Key)
	}

	if len(auditObjects) == 0 {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: "no audit snapshots found"})
		fmt.Fprintln(os.Stderr, "FAIL verify_s3: no audit snapshots found")
		return 1
	}

	// Find the most recent snapshot (last alphabetically)
	latest := auditObjects[len(auditObjects)-1]
	obj, err := mc.GetObject(ctx, auditBucket, latest, minio.GetObjectOptions{})
	if err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL verify_s3: get: %v\n", err)
		return 1
	}

	var snapshot struct {
		Ts       string `json:"ts"`
		Count    int    `json:"count"`
		Policies []any  `json:"policies"`
	}
	if err := json.NewDecoder(obj).Decode(&snapshot); err != nil {
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: err.Error()})
		fmt.Fprintf(os.Stderr, "FAIL verify_s3: decode: %v\n", err)
		return 1
	}
	if snapshot.Count == 0 || len(snapshot.Policies) == 0 {
		msg := fmt.Sprintf("empty snapshot: count=%d policies=%d", snapshot.Count, len(snapshot.Policies))
		ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "failed", Error: msg})
		fmt.Fprintf(os.Stderr, "FAIL verify_s3: %s\n", msg)
		return 1
	}

	ev.Steps = append(ev.Steps, stepResult{Step: "verify_s3", Status: "passed", Data: map[string]any{
		"objects_found":  len(auditObjects),
		"latest_key":    latest,
		"snapshot_count": snapshot.Count,
	}})
	fmt.Printf("PASS verify_s3: %d objects, latest count=%d\n", len(auditObjects), snapshot.Count)

	ev.Status = "passed"
	fmt.Println("=== ALL STEPS PASSED ===")
	return 0
}

func emitEvidence(ev evidence) {
	data, _ := json.MarshalIndent(ev, "", "  ")
	fmt.Println("EVIDENCE_JSON_START")
	fmt.Println(string(data))
	fmt.Println("EVIDENCE_JSON_END")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func replaceEnv(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[key]; ok {
			continue // skip, will be added below
		}
		result = append(result, entry)
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

