---
name: iac-safe-auditor
description: "Trigger: iac testing, terraform test, tflint, conftest, opa rego, checkov, localstack, cloud infrastructure testing, drift detection, infracost, mock provider. Safely audit and test Terraform/OpenTofu without live cloud risk or cost."
license: Apache-2.0
metadata:
  author: "alesierraalta"
  version: "0.1.0"
  requires_tpp: "0.4.1"
  scope: [terraform]
  auto_invoke: "Testing or auditing Terraform/OpenTofu: plan-JSON policy, mock unit tests, egress isolation, drift, cost"
---

## Activation Contract

Load when testing or auditing Terraform or OpenTofu configuration, plan JSON, policies evaluated against that plan, or cost projections for those configurations. Use for `terraform test`, `tflint`, `terraform validate`, Conftest/OPA, Checkov/Trivy configuration scans, LocalStack-backed checks, drift inspection, or Infracost review.

Scope is Terraform/OpenTofu with the AWS examples in the references. Kubernetes manifests, Helm charts, Dockerfiles and container images belong elsewhere (a future container skill); CloudFormation and Pulumi have no pattern here. Do not stretch this skill over them.
NOT for application runtime fault injection (`runtime-reliability-testing`), application security probing (`appsec-adversarial-auditor`), database semantics (`database-persistence-testing`), emulator container lifecycle (`docker-test-containers`), or dependency trust decisions for language packages (`dependency-legitimacy`).

## Hard Rules

1. **Zero Live Cloud Mutations**: NEVER run `terraform apply` or `terraform destroy` against a real provider account. Verification is dry-run, mock-driven, or against an owned ephemeral emulator.
2. **Endpoint and Egress Isolation**: Before any provider-backed command, override every provider endpoint to a local emulator or enforce deny-by-default egress. Dummy credentials are defense-in-depth, not network isolation. The one exception is drift inspection (rule 6), which is a separately authorized step.
3. **Four-Layer Pyramid**: L1 static (`terraform validate`, `tflint`, Checkov/Trivy); L2 policy on plan JSON (Conftest/OPA); L3 module unit tests (`terraform test` with `mock_provider`); L4 owned local emulator. Put each property at the layer that can observe it.
4. **Policy is code and must be tested**: every Rego rule ships with a fixture it must deny and one it must allow, run with `opa test` or `conftest verify`. A rule never shown to fail, or shown to fire on unrelated resources, is not evidence. Weaken a rule on purpose once and confirm its test goes red.
5. **FinOps Cost Gate**: require a versioned baseline, named owner, numeric tolerance, and an explicit policy for unpriced resources; fail closed otherwise.
6. **Drift needs read-only credentials**: `terraform plan -refresh-only -detailed-exitcode` (0 none, 2 drift, 1 error) is a live read of the real account, so it violates rule 2 unless a human explicitly authorizes it, with a read-only role, the named workspace, and awareness that the refresh takes the state lock. Never auto-apply a reconciliation. HCP Terraform health status is a separate signal.
7. **Plan JSON and state are secrets**: `terraform show -json` embeds sensitive values (passwords, generated keys). Write it outside the repository (or to a gitignored path), never commit or upload it as a CI artifact, never paste it into evidence rows or reports; quote only resource addresses and rule names. Delete it after the run.
8. **Pin what you run**: commit `.terraform.lock.hcl`; require bounded provider and module version constraints and immutable git refs (the shipped Rego denies unpinned providers/modules); pin scanner versions or CI actions by digest or commit SHA.
9. **Isolated Emulation Cleanup**: tear down only owned, labelled emulator resources selected by the run. Preview destructive cleanup and report ambiguous items instead of deleting them.

## Decision Gates

| Infrastructure scope | Required evidence | What this layer cannot prove |
| :--- | :--- | :--- |
| Syntax, types, variables | `terraform fmt -check`, `terraform validate`, `tflint` | Policy or runtime behavior. |
| Known misconfiguration | Checkov/Trivy under a stated severity and exception policy | Runtime behavior, org-specific rules, or a complete review; a clean scan is not proof of safety. |
| Organizational policy | `opa test` on the Rego, then `conftest test` on plan JSON | Unknown values, `lifecycle.ignore_changes`, dynamic blocks, cross-module references beyond the root module, IAM built from unresolved data sources. |
| Module logic | `terraform test` with `mock_provider` plus an `expect_failures` negative control | Real provider response shapes, computed values, service behavior. |
| Cloud-service integration | Local emulator with endpoint isolation | Only a subset of each service; IAM is not enforced by default. |
| Cost change | Infracost against the versioned baseline | List-price estimate, not a bill or usage forecast. |
| Drift | Authorized read-only refresh-only plan (rule 6) | Runtime health; masked by `ignore_changes`; says nothing about account security. |

## Execution Steps

1. **Static**: run `terraform fmt -check`, `terraform validate`, `tflint`, and the applicable Checkov or Trivy scan. Record severity policy and exceptions.
2. **Policy tests first**: run `opa test assets/ -v` (or `conftest verify`) and `opa check --strict`; do not evaluate a plan with a policy whose own tests fail. If a target property has no rule (for example trust policies with `Principal: "*"`), add a rule plus a deny and an allow fixture instead of trusting a scan.
3. **Policy on plan**: generate the plan JSON under rule 7, run `conftest test -p assets/ --rego-version v1 <plan.json>`. Public ingress covers `0.0.0.0/0` and `::/0` on every sensitive port, `protocol = "-1"`, inline `ingress`, `aws_security_group_rule` and `aws_vpc_security_group_ingress_rule`. S3 encryption lives in `aws_s3_bucket_server_side_encryption_configuration` (AWS provider v4+). IAM `Action: "*"` and `service:*` on `Resource: "*"` are denied. Tags are checked on taggable types only, honoring provider `default_tags`. Treat unknown values as unproven and test them at another layer.
4. **Mock unit tests**: `.tftest.hcl` cases for conditionals, `for_each`, outputs and variable validation; every positive run has a negative `expect_failures` run. Do not infer provider compatibility from a mock.
5. **FinOps projection**: `infracost diff` against the versioned baseline; stop unless owner, tolerance, pricing context and unpriced policy are present.
6. **Local emulation**: only when SDK behavior matters and the risk is not IAM. Point every used endpoint at the owned emulator and verify egress cannot reach a real endpoint.
7. **Drift**: only with explicit human authorization per rule 6; otherwise report it as not run.

## Output Contract

Report target and scope, commands with tool versions, per-layer findings and the "cannot prove" line for each layer used, `opa test` pass/fail counts, policy violations by rule and resource address (no plan contents), mock-test results including negative controls, cost baseline/delta/gate, drift status (run with authorization, or not run), emulator cleanup scope, and a `PASSED` or `FAILED` verdict naming the failing rule.

## References

- `references/patterns.md`: mock tests with a negative control, the Rego rule table, LocalStack endpoints, Infracost gate, drift probe.
- `assets/conftest-policy.rego`: OPA v1 policy for tags, public ingress, S3 encryption, IAM wildcards, version pinning.
- `assets/conftest-policy_test.rego`: 49 unit tests, deny and allow fixtures per rule.
