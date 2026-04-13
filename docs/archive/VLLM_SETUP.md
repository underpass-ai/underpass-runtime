# vLLM Setup & Troubleshooting

Practical ops guide for running vLLM on the underpass-runtime cluster.

## Hardware

- 4x NVIDIA RTX 3090 (24GB each)
- NVIDIA Driver: 590.48.01
- CUDA Version: 13.1

## Working Configuration

- Image: `docker.io/vllm/vllm-openai:v0.15.0-cu130`
- Model: `Qwen/Qwen3-8B` (bfloat16, 15.27 GiB VRAM)
- Required env vars: `LD_LIBRARY_PATH=/usr/lib`, `VLLM_ATTENTION_BACKEND=FLASHINFER`
- Args: `--model=Qwen/Qwen3-8B --tensor-parallel-size=1 --max-model-len=4096 --gpu-memory-utilization=0.85 --trust-remote-code --host=0.0.0.0`
- Startup time: ~3 minutes (model download cached, CUDA graph compilation)

## Common Issues

### ImageInspectError: short name mode enforcing

- **Problem**: Image `vllm/vllm-openai:v0.8.5` fails with "short name mode is enforcing, but image name returns ambiguous list"
- **Fix**: Use full registry path `docker.io/vllm/vllm-openai:v0.15.0-cu130`

### CUDA Compatibility

- **Problem**: vLLM images without `-cu130` suffix may not work with NVIDIA Driver 590.x / CUDA 13.1
- **Fix**: Use the `-cu130` tagged image: `v0.15.0-cu130`
- **Critical**: Set `LD_LIBRARY_PATH=/usr/lib` in container env

### vLLM not accepting external connections

- **Problem**: vLLM starts, `localhost:8000` works from within pod, but Service/Ingress can't reach it
- **Fix**: Add `--host=0.0.0.0` to container args. vLLM v0.15+ may default to localhost binding.

### Pod evictions during image pull

- **Problem**: Multiple evicted pods when pulling large vLLM image (~10GB)
- **Fix**: Ensure node has sufficient ephemeral storage. Clean up old images: `crictl rmi --prune`

## Networking

### HTTPS via Ingress

- Ingress: nginx class
- Host: `llm.underpassai.com`
- TLS: cert-manager with `letsencrypt-prod-r53` ClusterIssuer (Route53 DNS challenge)
- DNS: Route53 A record -> MetalLB IP `192.168.1.241`

### mTLS (Client Certificate Verification)

- nginx annotation `auth-tls-verify-client: on`
- CA secret: `vllm-client-ca` (contains CA that signed client certs)
- Client cert: `vllm-client-cert` (CN=underpass-demo, O=Underpass AI)
- Without client cert: HTTP 400
- With valid client cert: HTTP 200

### Generate new client certificates

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -sha256 -days 365 -subj "/CN=vLLM Client CA/O=Underpass AI" -out ca.crt

# Generate client cert
openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=underpass-demo/O=Underpass AI" -out client.csr
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -sha256 -days 365 -out client.crt

# Update CA secret
kubectl create secret generic vllm-client-ca --from-file=ca.crt=ca.crt -n underpass-runtime --dry-run=client -o yaml | kubectl apply -f -

# Update client cert secret
kubectl create secret tls vllm-client-cert --cert=client.crt --key=client.key -n underpass-runtime --dry-run=client -o yaml | kubectl apply -f -
```

### Test

```bash
# Without client cert (should fail 400)
curl -s -o /dev/null -w "%{http_code}" https://llm.underpassai.com/v1/models

# With client cert (should succeed 200)
curl -s --cert client.crt --key client.key https://llm.underpassai.com/v1/models

# Inference test
curl -s --cert client.crt --key client.key https://llm.underpassai.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"Qwen/Qwen3-8B","messages":[{"role":"user","content":"Hello"}],"max_tokens":10}'
```

## Helm Values

```yaml
vllm:
  ingress:
    enabled: true
    host: llm.underpassai.com
    clusterIssuer: letsencrypt-prod-r53
    tlsSecret: vllm-tls
    mtls:
      enabled: true
      clientCaSecret: vllm-client-ca
```

## K8s Secrets

| Secret | Namespace | Contents | Purpose |
|--------|-----------|----------|---------|
| vllm-tls | underpass-runtime | tls.crt, tls.key | Server TLS (auto-managed by cert-manager) |
| vllm-client-ca | underpass-runtime | ca.crt | CA to verify client certificates |
| vllm-client-cert | underpass-runtime | tls.crt, tls.key | Client cert for demo TUI |
