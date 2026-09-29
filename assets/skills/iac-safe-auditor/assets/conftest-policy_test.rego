package main

# Unit tests for conftest-policy.rego. Run: opa test assets/ -v  (or conftest verify -p assets/)
# Each rule has at least one fixture that must be denied and one that must not,
# so a rule that never fires or fires on everything fails here.

import rego.v1

# ---------------------------------------------------------------- fixtures

change(type, name, after) := {
	"address": sprintf("%s.%s", [type, name]),
	"type": type,
	"name": name,
	"change": {"actions": ["create"], "after": after, "after_unknown": {}},
}

plan(changes) := {"resource_changes": changes}

msgs(sub) := {m |
	some m in deny
	contains(m, sub)
}

full_tags := {"Environment": "prod", "Owner": "platform", "ManagedBy": "terraform"}

encrypted_inline := [{"rule": [{"apply_server_side_encryption_by_default": [{"sse_algorithm": "AES256"}]}]}]

sse_change(bucket, alg) := change(
	"aws_s3_bucket_server_side_encryption_configuration", "enc",
	{"bucket": bucket, "rule": [{"apply_server_side_encryption_by_default": [{"sse_algorithm": alg}]}]},
)

policy_json(statement) := json.marshal({"Version": "2012-10-17", "Statement": statement})

iam(statement) := plan([change("aws_iam_policy", "p", {"policy": policy_json(statement)})])

# ---------------------------------------------------------------- mandatory tags

test_tags_missing_denied if {
	count(msgs("missing mandatory tags")) == 1 with input as plan([change("aws_instance", "web", {"tags": {"Owner": "x"}})])
}

test_tags_null_denied if {
	count(msgs("missing mandatory tags")) == 1 with input as plan([change("aws_instance", "web", {"tags": null})])
}

test_tags_complete_allowed if {
	count(msgs("missing mandatory tags")) == 0 with input as plan([change("aws_instance", "web", {"tags": full_tags})])
}

test_tags_not_required_on_untaggable_type if {
	# aws_security_group_rule has no tags attribute; it must not fire.
	count(msgs("missing mandatory tags")) == 0 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": 443, "to_port": 443, "protocol": "tcp", "cidr_blocks": ["10.0.0.0/8"]})])
}

test_tags_all_from_default_tags_allowed if {
	count(msgs("missing mandatory tags")) == 0 with input as plan([change("aws_instance", "web", {"tags": {}, "tags_all": full_tags})])
}

test_tags_all_present_but_incomplete_denied if {
	count(msgs("missing mandatory tags")) == 1 with input as plan([change("aws_instance", "web", {"tags": {}, "tags_all": {"Environment": "prod"}})])
}

test_tags_unknown_with_default_tags_not_guessed if {
	rc := object.union(change("aws_instance", "web", {"tags": {}}), {"change": {"actions": ["create"], "after": {"tags": {}}, "after_unknown": {"tags_all": true}}})
	inp := {"resource_changes": [rc], "configuration": {"provider_config": {"aws": {"version_constraint": "~> 5.0", "expressions": {"default_tags": [{"tags": {}}]}}}}}
	count(msgs("missing mandatory tags")) == 0 with input as inp
}

test_tags_unknown_without_default_tags_denied if {
	rc := object.union(change("aws_instance", "web", {"tags": {}}), {"change": {"actions": ["create"], "after": {"tags": {}}, "after_unknown": {"tags_all": true}}})
	count(msgs("missing mandatory tags")) == 1 with input as {"resource_changes": [rc]}
}

test_tags_delete_not_checked if {
	rc := object.union(change("aws_instance", "web", {"tags": {}}), {"change": {"actions": ["delete"], "after": null}})
	count(msgs("missing mandatory tags")) == 0 with input as {"resource_changes": [rc]}
}

# ---------------------------------------------------------------- public ingress

sg_inline(ingress) := plan([change("aws_security_group", "sg", {"ingress": [ingress]})])

test_ingress_inline_ipv4_ssh_denied if {
	count(msgs("sensitive ports")) == 1 with input as sg_inline({"from_port": 22, "to_port": 22, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": []})
}

test_ingress_inline_ipv6_rdp_denied if {
	# IPv6 must be covered for every sensitive port, not only SSH.
	count(msgs("sensitive ports")) == 1 with input as sg_inline({"from_port": 3389, "to_port": 3389, "protocol": "tcp", "cidr_blocks": [], "ipv6_cidr_blocks": ["::/0"]})
}

test_ingress_sg_rule_ipv6_postgres_denied if {
	count(msgs("sensitive ports")) == 1 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": 5432, "to_port": 5432, "protocol": "tcp", "cidr_blocks": null, "ipv6_cidr_blocks": ["::/0"]})])
}

test_ingress_vpc_rule_ipv4_mysql_denied if {
	count(msgs("sensitive ports")) == 1 with input as plan([change("aws_vpc_security_group_ingress_rule", "r", {"from_port": 3306, "to_port": 3306, "ip_protocol": "tcp", "cidr_ipv4": "0.0.0.0/0", "cidr_ipv6": null})])
}

test_ingress_vpc_rule_ipv6_redis_denied if {
	count(msgs("sensitive ports")) == 1 with input as plan([change("aws_vpc_security_group_ingress_rule", "r", {"from_port": 6379, "to_port": 6379, "ip_protocol": "tcp", "cidr_ipv4": null, "cidr_ipv6": "::/0"})])
}

test_ingress_wide_range_denied if {
	count(msgs("sensitive ports")) == 1 with input as sg_inline({"from_port": 0, "to_port": 65535, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": []})
}

test_ingress_all_traffic_inline_denied_and_marked_verify if {
	count(msgs("all protocols and ports")) == 1 with input as sg_inline({"from_port": 0, "to_port": 0, "protocol": "-1", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": []})
	count(msgs("verify the intent")) == 1 with input as sg_inline({"from_port": 0, "to_port": 0, "protocol": "-1", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": []})
}

test_ingress_all_traffic_vpc_rule_null_ports_denied if {
	count(msgs("all protocols and ports")) == 1 with input as plan([change("aws_vpc_security_group_ingress_rule", "r", {"from_port": null, "to_port": null, "ip_protocol": "-1", "cidr_ipv4": "0.0.0.0/0", "cidr_ipv6": null})])
}

test_ingress_https_public_allowed if {
	count(deny) == 0 with input as sg_inline({"from_port": 443, "to_port": 443, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": ["::/0"]})
}

test_ingress_ssh_private_cidr_allowed if {
	count(deny) == 0 with input as sg_inline({"from_port": 22, "to_port": 22, "protocol": "tcp", "cidr_blocks": ["10.0.0.0/8"], "ipv6_cidr_blocks": []})
}

test_ingress_all_traffic_private_cidr_allowed if {
	count(deny) == 0 with input as sg_inline({"from_port": 0, "to_port": 0, "protocol": "-1", "cidr_blocks": ["10.0.0.0/8"], "ipv6_cidr_blocks": []})
}

test_ingress_egress_rule_not_checked if {
	count(deny) == 0 with input as plan([change("aws_security_group_rule", "r", {"type": "egress", "from_port": 22, "to_port": 22, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"]})])
}

test_ingress_non_sensitive_port_allowed if {
	count(deny) == 0 with input as sg_inline({"from_port": 8080, "to_port": 8080, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"], "ipv6_cidr_blocks": []})
}

# ---------------------------------------------------------------- S3 encryption

test_s3_unencrypted_denied if {
	count(msgs("encryption")) == 1 with input as plan([change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags})])
}

test_s3_inline_encrypted_allowed if {
	count(msgs("encryption")) == 0 with input as plan([change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags, "server_side_encryption_configuration": encrypted_inline})])
}

test_s3_separate_resource_by_literal_name_allowed if {
	count(deny) == 0 with input as plan([change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags}), sse_change("data", "aws:kms")])
}

test_s3_separate_resource_by_reference_allowed if {
	inp := {
		"resource_changes": [
			change("aws_s3_bucket", "b", {"bucket": null, "tags": full_tags}),
			sse_change(null, "AES256"),
		],
		"configuration": {"root_module": {"resources": [{"type": "aws_s3_bucket_server_side_encryption_configuration", "name": "enc", "expressions": {"bucket": {"references": ["aws_s3_bucket.b.id", "aws_s3_bucket.b"]}}}]}},
	}
	count(msgs("encryption")) == 0 with input as inp
}

test_s3_updated_bucket_with_unchanged_encryption_allowed if {
	enc := object.union(sse_change("data", "AES256"), {"change": {"actions": ["no-op"], "after": {"bucket": "data", "rule": [{"apply_server_side_encryption_by_default": [{"sse_algorithm": "AES256"}]}]}}})
	bucket := object.union(change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags}), {"change": {"actions": ["update"], "after": {"bucket": "data", "tags": full_tags}}})
	count(msgs("encryption")) == 0 with input as plan([bucket, enc])
}

test_s3_separate_resource_wrong_algorithm_denied if {
	count(msgs("encryption")) == 1 with input as plan([change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags}), sse_change("data", "none")])
}

test_s3_separate_resource_for_other_bucket_denied if {
	count(msgs("encryption")) == 1 with input as plan([change("aws_s3_bucket", "b", {"bucket": "data", "tags": full_tags}), sse_change("someone-elses", "AES256")])
}

# ---------------------------------------------------------------- IAM wildcards

test_iam_action_star_denied if {
	count(msgs("every AWS API")) == 1 with input as iam([{"Effect": "Allow", "Action": "*", "Resource": "arn:aws:s3:::b"}])
}

test_iam_action_star_in_list_denied if {
	count(msgs("every AWS API")) == 1 with input as iam([{"Effect": "Allow", "Action": ["s3:GetObject", "*"], "Resource": "*"}])
}

test_iam_single_statement_object_denied if {
	count(msgs("every AWS API")) == 1 with input as iam({"Effect": "Allow", "Action": "*", "Resource": "*"})
}

test_iam_service_wildcard_on_resource_star_denied if {
	count(msgs("service-wide action")) == 1 with input as iam([{"Effect": "Allow", "Action": ["s3:*"], "Resource": "*"}])
}

test_iam_describe_on_resource_star_allowed if {
	count(deny) == 0 with input as iam([{"Effect": "Allow", "Action": ["ec2:Describe*"], "Resource": "*"}])
}

test_iam_scoped_allowed if {
	count(deny) == 0 with input as iam([{"Effect": "Allow", "Action": ["s3:GetObject"], "Resource": "arn:aws:s3:::b/*"}])
}

test_iam_service_wildcard_on_scoped_resource_allowed if {
	count(deny) == 0 with input as iam([{"Effect": "Allow", "Action": "s3:*", "Resource": "arn:aws:s3:::b/*"}])
}

test_iam_deny_statement_not_flagged if {
	count(deny) == 0 with input as iam([{"Effect": "Deny", "Action": "*", "Resource": "*"}])
}

test_iam_unknown_policy_does_not_crash_or_fire if {
	count(deny) == 0 with input as plan([change("aws_iam_policy", "p", {"policy": null})])
}

# ---------------------------------------------------------------- version pinning

cfg_provider(p) := {"configuration": {"provider_config": p}}

cfg_modules(m) := {"configuration": {"root_module": {"module_calls": m}}}

test_provider_without_constraint_denied if {
	count(msgs("no version constraint")) == 1 with input as cfg_provider({"aws": {"name": "aws"}})
}

test_provider_bounded_constraint_allowed if {
	count(deny) == 0 with input as cfg_provider({"aws": {"name": "aws", "version_constraint": "~> 5.0"}})
}

test_provider_unbounded_constraint_denied if {
	count(msgs("no upper bound")) == 1 with input as cfg_provider({"aws": {"name": "aws", "version_constraint": ">= 5.0"}})
}

test_provider_builtin_terraform_ignored if {
	count(deny) == 0 with input as cfg_provider({"terraform": {"name": "terraform"}})
}

test_module_registry_without_version_denied if {
	count(msgs("Registry module")) == 1 with input as cfg_modules({"vpc": {"source": "terraform-aws-modules/vpc/aws"}})
}

test_module_registry_unbounded_denied if {
	count(msgs("no upper bound")) == 1 with input as cfg_modules({"vpc": {"source": "terraform-aws-modules/vpc/aws", "version_constraint": ">= 5.0"}})
}

test_module_registry_pinned_allowed if {
	count(deny) == 0 with input as cfg_modules({"vpc": {"source": "terraform-aws-modules/vpc/aws", "version_constraint": "~> 5.1"}})
}

test_module_git_branch_ref_denied if {
	count(msgs("git source")) == 1 with input as cfg_modules({"m": {"source": "git::https://github.com/org/mod.git?ref=main"}})
}

test_module_git_without_ref_denied if {
	count(msgs("git source")) == 1 with input as cfg_modules({"m": {"source": "git::https://example.com/org/mod.git"}})
}

test_module_git_commit_ref_allowed if {
	count(deny) == 0 with input as cfg_modules({"m": {"source": "git::https://github.com/org/mod.git?ref=0123456789abcdef0123456789abcdef01234567"}})
}

test_module_local_path_allowed if {
	count(deny) == 0 with input as cfg_modules({"m": {"source": "./modules/net"}})
}

# ---------------------------------------------------------------- fail closed on unreadable resources

test_after_null_denied if {
	count(msgs("change.after is not an object")) == 1 with input as plan([change("aws_instance", "web", null)])
}

test_after_string_denied if {
	count(msgs("change.after is not an object")) == 1 with input as plan([change("aws_instance", "web", "oops")])
}

test_after_object_not_flagged if {
	count(msgs("change.after is not an object")) == 0 with input as plan([change("aws_instance", "web", {"tags": full_tags})])
}

test_sg_rule_missing_type_denied if {
	count(msgs("has no type")) == 1 with input as plan([change("aws_security_group_rule", "r", {"from_port": 22, "to_port": 22, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"]})])
}

test_sg_rule_with_type_not_flagged if {
	count(msgs("has no type")) == 0 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": 443, "to_port": 443, "protocol": "tcp", "cidr_blocks": ["10.0.0.0/8"]})])
}

test_public_unknown_ports_denied if {
	count(msgs("unknown ports")) == 1 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": null, "to_port": null, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"]})])
}

test_public_udp_unknown_to_port_denied if {
	count(msgs("unknown ports")) == 1 with input as plan([change("aws_vpc_security_group_ingress_rule", "r", {"from_port": 53, "ip_protocol": "udp", "cidr_ipv4": "0.0.0.0/0"})])
}

test_private_unknown_ports_allowed if {
	count(msgs("unknown ports")) == 0 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": null, "to_port": null, "protocol": "tcp", "cidr_blocks": ["10.0.0.0/8"]})])
}

test_public_known_safe_ports_allowed if {
	count(deny) == 0 with input as plan([change("aws_security_group_rule", "r", {"type": "ingress", "from_port": 443, "to_port": 443, "protocol": "tcp", "cidr_blocks": ["0.0.0.0/0"]})])
}
