.PHONY: help lint lint-adr-status lint-adr-numbers

help:
	@echo "Available targets:"
	@echo "  help             - Show this help message"
	@echo "  lint             - Run all linting and validation"
	@echo "  lint-adr-status  - Validate ADR statuses in all ADR files"
	@echo "  lint-adr-numbers - Check for duplicate ADR numeric identifiers"

lint: lint-adr-status lint-adr-numbers

lint-adr-status:
	@./hack/lint-adr-status

lint-adr-numbers:
	@./hack/lint-adr-numbers
