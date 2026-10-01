package eval

// Domain classifies an Issue by the kind of system property it violates.
type Domain string

const (
	Security     Domain = "security"
	Correctness  Domain = "correctness"
	Concurrency  Domain = "concurrency"
	Performance  Domain = "performance"
	Resilience   Domain = "resilience"
	Data         Domain = "data"
	APICompat    Domain = "api-compat"
	AILLM        Domain = "ai-llm"
	Architecture Domain = "architecture"
)

var domains = [...]Domain{Security, Correctness, Concurrency, Performance, Resilience, Data, APICompat, AILLM, Architecture}

// ParseDomain parses a canonical domain value.
func ParseDomain(s string) (Domain, bool) {
	for _, domain := range domains {
		if s == string(domain) {
			return domain, true
		}
	}
	return "", false
}

// AllDomains returns the domain taxonomy in specification order.
func AllDomains() []Domain {
	return append([]Domain(nil), domains[:]...)
}
