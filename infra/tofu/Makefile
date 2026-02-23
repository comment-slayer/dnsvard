.PHONY: plan apply

plan:
	@test -n "$(name)" || (printf "usage: make plan name=<plan-name>\n" >&2; exit 1)
	@tofu plan -out="$(name).tfplan"

apply:
	@test -n "$(name)" || (printf "usage: make apply name=<plan-name>\n" >&2; exit 1)
	@test -f "$(name).tfplan" || (printf "missing plan file: %s.tfplan\n" "$(name)" >&2; exit 1)
	@tofu apply "$(name).tfplan"
