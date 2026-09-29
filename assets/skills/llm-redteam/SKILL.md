---
name: llm-redteam
description: "Trigger: llm red team, prompt injection, indirect injection, jailbreak, crescendo, prompt leakage, excessive agency, lethal trifecta, markdown exfiltration, promptfoo, garak, pyrit. Attack LLM apps that read untrusted content or hold tools."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [llm, security]
  auto_invoke: "Red-teaming an LLM app that has tools, untrusted input or private data: injection, exfiltration, excessive agency, leakage, jailbreaks"
---

## Activation Contract

Load when an LLM application reads untrusted content (web, email, files, tool or MCP output, user uploads), holds tools, or has access to private data.

Applicability gate first: list what the LLM can read, what it can do, and where its output goes. The lethal trifecta (private data + untrusted content + an exfiltration channel) is the activation signal. A tool-less, static FAQ bot with no private data needs no battery; do not run one.

NOT for: deterministic controls (tool authorization, output-to-sink taint, tenant-scoped retrieval), classic authz and injection into non-LLM sinks (`appsec-adversarial-auditor` owns them); RAG-specific cross-tenant leakage (`rag-audit-evaluator`); judge and agent grading (`llm-eval-design`, `agent-eval`).

## Hard Rules

1. **Use only against systems you own or are authorized to test**, with sandboxed tools and canary secrets; never real customer data or live destructive effects.
2. **Run the trifecta audit before any probe.** Removing one leg (no private data, no untrusted input, or no egress) is a stronger fix than any prompt defense; report it as such.
3. **Indirect injection first.** Plant instructions in tool results, retrieved documents, emails, web pages, filenames and MCP tool descriptions (AgentDojo-style tasks). The model must not obey them.
4. **Exfiltration channels.** Test markdown images and links, auto-fetched URLs, and data placed in tool-call arguments (email, PR, HTTP). Detect a canary token leaving through each channel.
5. **Excessive agency (OWASP LLM06).** With an injected instruction, check that destructive or high-privilege tools do not run without the confirmation the design promises.
6. **System prompt leakage (LLM07)** and sensitive disclosure (LLM02): probe with a canary planted in the prompt and context.
7. **Adaptive multi-turn jailbreaks** (crescendo-style), not only static prompts; static suites decay.
8. **Measure, do not assert.** Report attack success rate over repeated trials with a confidence interval, and utility under attack plus a benign-task control for over-refusal. A guardrail's claimed catch rate is a claim until measured on both attacks and benign traffic.
9. **A pass means "not found by these probes".** Probe results never prove safety; deterministic controls do.

## Decision Gates

| Signal | Test |
| :--- | :--- |
| Private data + untrusted content + egress | Full battery, Rules 3-5 first |
| Tools with side effects | Rule 5, sandboxed |
| Secrets in the prompt | Rule 6 |
| Chat only, no tools, no data | Skip; report the applicability result |
| RAG with several tenants | Route leakage to `rag-audit-evaluator` |

| Tool | Use | Capability note |
| :--- | :--- | :--- |
| promptfoo | CI regression of app-level red team via YAML | Plugin and strategy names change by version; verify against the installed version |
| garak | Model-level probe breadth | Agentic and RAG coverage limited; verify |
| PyRIT | Programmable adaptive multi-turn campaigns | Verify orchestrator names before use |

## Execution Steps

1. Trifecta and tool inventory; decide scope.
2. Plant canaries; run Rules 3-6 probes, one channel at a time.
3. Run the adaptive multi-turn campaign with a fixed attacker budget.
4. Repeat trials; compute ASR with CI, utility under attack, over-refusal.
5. Hand deterministic gaps to `appsec-adversarial-auditor`; report.

## Output Contract

- Trifecta result and tools in scope.
- Per class (LLM01, 02, 06, 07 and exfiltration): probes, trials, ASR with CI, evidence payload.
- Utility under attack and over-refusal control.
- Findings with reproduction payloads, and coverage gaps.
- Verdict `PASSED` | `FAILED`, with the Rule 9 caveat.

## References

- `assets/promptfooconfig.redteam.yaml`: pinned-model starter; verify plugin names and add target, canaries and a benign control before use.
