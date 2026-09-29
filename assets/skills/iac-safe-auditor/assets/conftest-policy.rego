package main

# Conftest/OPA policy over `terraform show -json tfplan` output (OPA v1 syntax).
# Every rule has positive and negative fixtures in conftest-policy_test.rego.
# Scope: AWS provider resources in Terraform/OpenTofu plan JSON. Rules only see
# values that are KNOWN at plan time; unknown values are listed in SKILL.md.

import rego.v1

# ---------------------------------------------------------------- helpers

# Only create and update are policy-relevant. Deletes are not checked.
is_create_or_update(rc) if {
	some a in rc.change.actions
	a in {"create", "update"}
}

managed_changes contains rc if {
	some rc in input.resource_changes
	is_create_or_update(rc)
}

# Resources that exist after apply, including untouched ("no-op") ones, so a
# bucket update can be linked to an encryption resource that did not change.
planned contains rc if {
	some rc in input.resource_changes
	rc.change.after != null
}

as_list(x) := x if is_array(x)

as_list(x) := [] if x == null

as_list(x) := [x] if {
	x != null
	not is_array(x)
}

as_object(x) := x if is_object(x)

as_object(x) := {} if not is_object(x)

# ---------------------------------------------------------------- mandatory tags

mandatory_tags := {"Environment", "Owner", "ManagedBy"}

# Schema-driven: only resources whose plan carries a tags or tags_all attribute
# are taggable. aws_security_group_rule, aws_s3_bucket_policy, aws_route, ...
# have neither and are never checked.
taggable(rc) if {
	is_object(rc.change.after)
	"tags" in object.keys(rc.change.after)
}

taggable(rc) if {
	is_object(rc.change.after)
	"tags_all" in object.keys(rc.change.after)
}

# Fail closed: a create/update whose planned state cannot be read is denied,
# never skipped. Every rule below assumes an object `after`.
deny contains msg if {
	some rc in managed_changes
	not is_object(rc.change.after)
	msg := sprintf("Resource '%v' cannot be evaluated: change.after is not an object.", [rc.address])
}

# tags_all merges provider default_tags with resource tags; evaluate the union.
effective_tags(rc) := object.union(
	as_object(object.get(rc.change.after, "tags_all", {})),
	as_object(object.get(rc.change.after, "tags", {})),
)

default_tags_configured if {
	some _, p in object.get(input.configuration, "provider_config", {})
	object.get(p, ["expressions", "default_tags"], null) != null
}

# tags_all is unknown until apply when default_tags reference unknown values.
# The plan then cannot prove the tags either way; do not guess.
tags_unprovable(rc) if {
	object.get(rc.change, ["after_unknown", "tags_all"], false) == true
	default_tags_configured
}

deny contains msg if {
	some rc in managed_changes
	taggable(rc)
	not tags_unprovable(rc)
	tags := effective_tags(rc)
	missing := {t | some t in mandatory_tags; object.get(tags, t, "") == ""}
	count(missing) > 0
	msg := sprintf("Resource '%v' is missing mandatory tags: %v", [rc.address, sort(missing)])
}

# ---------------------------------------------------------------- public ingress

public_cidrs := {"0.0.0.0/0", "::/0"}

# Ports that must never be reachable from the whole internet.
sensitive_ports := {22, 2375, 3306, 3389, 5432, 5984, 6379, 9200, 9300, 11211, 27017, 1433}

# Normalised ingress rule: {address, from, to, proto, cidrs}. All three AWS
# shapes feed the same guard so a rule cannot be bypassed by switching resource.

# Inline ingress blocks on aws_security_group.
ingress_rules contains r if {
	some rc in managed_changes
	rc.type == "aws_security_group"
	some i in as_list(object.get(rc.change.after, "ingress", null))
	r := {
		"address": rc.address,
		"ports_known": ports_known(i),
		"from": num(object.get(i, "from_port", 0)),
		"to": num(object.get(i, "to_port", 0)),
		"proto": object.get(i, "protocol", ""),
		"cidrs": {c |
			some c in array.concat(
				as_list(object.get(i, "cidr_blocks", null)),
				as_list(object.get(i, "ipv6_cidr_blocks", null)),
			)
		},
	}
}

# Standalone aws_security_group_rule with type = "ingress".
ingress_rules contains r if {
	some rc in managed_changes
	rc.type == "aws_security_group_rule"
	is_object(rc.change.after)
	rc.change.after.type == "ingress"
	a := rc.change.after
	r := {
		"address": rc.address,
		"ports_known": ports_known(a),
		"from": num(object.get(a, "from_port", 0)),
		"to": num(object.get(a, "to_port", 0)),
		"proto": object.get(a, "protocol", ""),
		"cidrs": {c |
			some c in array.concat(
				as_list(object.get(a, "cidr_blocks", null)),
				as_list(object.get(a, "ipv6_cidr_blocks", null)),
			)
		},
	}
}

# aws_vpc_security_group_ingress_rule: cidr_ipv4 / cidr_ipv6 are strings and
# ports are null when ip_protocol = "-1".
ingress_rules contains r if {
	some rc in managed_changes
	rc.type == "aws_vpc_security_group_ingress_rule"
	a := rc.change.after
	r := {
		"address": rc.address,
		"ports_known": ports_known(a),
		"from": num(object.get(a, "from_port", 0)),
		"to": num(object.get(a, "to_port", 0)),
		"proto": object.get(a, "ip_protocol", ""),
		"cidrs": {c |
			some c in array.concat(
				as_list(object.get(a, "cidr_ipv4", null)),
				as_list(object.get(a, "cidr_ipv6", null)),
			)
		},
	}
}

# Ports are known only when both are numbers. null or absent (unknown until
# apply) is never coerced to a safe value.
ports_known(o) := true if {
	is_number(object.get(o, "from_port", null))
	is_number(object.get(o, "to_port", null))
} else := false

num(x) := x if is_number(x)

num(x) := 0 if not is_number(x)

public_ingress contains r if {
	some r in ingress_rules
	some c in r.cidrs
	c in public_cidrs
}

exposed_ports(r) := {p |
	some p in sensitive_ports
	r.from <= p
	p <= r.to
}

# protocol "-1" is all traffic: ports are 0/0 or null, so a port comparison
# would miss it. Denied outright; "verify" tells the reader to confirm intent.
deny contains msg if {
	some r in public_ingress
	r.proto == "-1"
	msg := sprintf("Resource '%v' opens all protocols and ports to the internet (protocol \"-1\"); verify the intent, denied unless waived.", [r.address])
}

# Fail closed on unknown ports: treat them as possibly including a sensitive port.
deny contains msg if {
	some r in public_ingress
	r.proto in {"tcp", "6", "udp", "17"}
	not r.ports_known
	msg := sprintf("Resource '%v' opens tcp/udp to the internet with unknown ports; cannot exclude sensitive ports.", [r.address])
}

# A standalone rule without a readable type cannot be classified.
deny contains msg if {
	some rc in managed_changes
	rc.type == "aws_security_group_rule"
	is_object(rc.change.after)
	not is_string(object.get(rc.change.after, "type", null))
	msg := sprintf("Resource '%v' cannot be evaluated: aws_security_group_rule has no type.", [rc.address])
}

deny contains msg if {
	some r in public_ingress
	r.proto in {"tcp", "6"}
	r.ports_known
	ports := exposed_ports(r)
	count(ports) > 0
	msg := sprintf("Resource '%v' exposes sensitive ports %v to the internet (0.0.0.0/0 or ::/0).", [r.address, sort(ports)])
}

# ---------------------------------------------------------------- S3 encryption

allowed_s3_algorithms := {"aws:kms", "AES256"}

valid_sse_rule(rule) if {
	some d in as_list(object.get(rule, "apply_server_side_encryption_by_default", null))
	d.sse_algorithm in allowed_s3_algorithms
}

# Legacy inline attribute (AWS provider < 4, deprecated afterwards).
inline_encrypted(rc) if {
	some c in as_list(object.get(rc.change.after, "server_side_encryption_configuration", null))
	some rule in as_list(object.get(c, "rule", null))
	valid_sse_rule(rule)
}

# AWS provider v4+: separate aws_s3_bucket_server_side_encryption_configuration.
separate_encrypted(rc) if {
	some sc in planned
	sc.type == "aws_s3_bucket_server_side_encryption_configuration"
	some rule in as_list(object.get(sc.change.after, "rule", null))
	valid_sse_rule(rule)
	bucket_linked(rc, sc)
}

# Linked by literal bucket name known at plan time...
bucket_linked(rc, sc) if {
	name := object.get(rc.change.after, "bucket", null)
	name != null
	object.get(sc.change.after, "bucket", null) == name
}

# ...or by a configuration reference (bucket = aws_s3_bucket.x.id). Root module
# only: a reference inside a child module is not resolved here.
bucket_linked(rc, sc) if {
	not rc.module_address
	some cfg in input.configuration.root_module.resources
	cfg.type == sc.type
	cfg.name == sc.name
	base := sprintf("%s.%s", [rc.type, rc.name])
	some ref in cfg.expressions.bucket.references
	ref in {base, concat(".", [base, "id"]), concat(".", [base, "bucket"])}
}

deny contains msg if {
	some rc in managed_changes
	rc.type == "aws_s3_bucket"
	not inline_encrypted(rc)
	not separate_encrypted(rc)
	msg := sprintf("Resource '%v' has no aws:kms or AES256 encryption (inline or via aws_s3_bucket_server_side_encryption_configuration); verify child-module and unknown-value links.", [rc.address])
}

# ---------------------------------------------------------------- IAM wildcards

iam_policy_types := {"aws_iam_policy", "aws_iam_role_policy", "aws_iam_user_policy", "aws_iam_group_policy"}

# {address, statement} for every Allow statement whose policy JSON is known at
# plan time. An unknown or null policy yields nothing (see SKILL.md limits).
allow_statements contains s if {
	some rc in managed_changes
	rc.type in iam_policy_types
	p := rc.change.after.policy
	is_string(p)
	doc := json.unmarshal(p)
	some st in as_list(object.get(doc, "Statement", null))
	st.Effect == "Allow"
	s := {"address": rc.address, "statement": st}
}

deny contains msg if {
	some s in allow_statements
	"*" in as_list(object.get(s.statement, "Action", null))
	msg := sprintf("Resource '%v' allows Action \"*\" (every AWS API).", [s.address])
}

# Resource "*" alone is common (ec2:Describe*); it is denied only when paired
# with a service-wide action such as "s3:*".
deny contains msg if {
	some s in allow_statements
	"*" in as_list(object.get(s.statement, "Resource", null))
	some a in as_list(object.get(s.statement, "Action", null))
	regex.match(`^[A-Za-z0-9-]+:\*$`, a)
	msg := sprintf("Resource '%v' allows service-wide action '%v' on Resource \"*\".", [s.address, a])
}

# ---------------------------------------------------------------- version pinning

unbounded(c) if regex.match(`^\s*>=?\s*[0-9][0-9A-Za-z.\-]*\s*$`, c)

# The built-in "terraform" provider (terraform_data) has no registry version.
deny contains msg if {
	some name, p in object.get(input.configuration, "provider_config", {})
	name != "terraform"
	not p.version_constraint
	msg := sprintf("Provider '%v' has no version constraint; pin it in required_providers.", [name])
}

deny contains msg if {
	some name, p in object.get(input.configuration, "provider_config", {})
	unbounded(p.version_constraint)
	msg := sprintf("Provider '%v' constraint '%v' has no upper bound; use a bounded constraint such as ~>.", [name, p.version_constraint])
}

local_source(s) if startswith(s, "./")

local_source(s) if startswith(s, "../")

git_source(s) if contains(s, "git::")

git_source(s) if contains(s, "github.com")

git_source(s) if startswith(s, "git@")

git_ref_pinned(s) if {
	regex.match(`[?&]ref=[^&]+`, s)
	not regex.match(`[?&]ref=(main|master|HEAD)(&|$)`, s)
}

# Root-module calls only; nested module calls are not walked here.
deny contains msg if {
	some name, m in object.get(object.get(input.configuration, "root_module", {}), "module_calls", {})
	git_source(m.source)
	not git_ref_pinned(m.source)
	msg := sprintf("Module '%v' uses a git source without an immutable ref (missing or main/master/HEAD).", [name])
}

deny contains msg if {
	some name, m in object.get(object.get(input.configuration, "root_module", {}), "module_calls", {})
	not local_source(m.source)
	not git_source(m.source)
	not m.version_constraint
	msg := sprintf("Registry module '%v' has no version constraint.", [name])
}

deny contains msg if {
	some name, m in object.get(object.get(input.configuration, "root_module", {}), "module_calls", {})
	not local_source(m.source)
	not git_source(m.source)
	unbounded(m.version_constraint)
	msg := sprintf("Registry module '%v' constraint '%v' has no upper bound.", [name, m.version_constraint])
}
