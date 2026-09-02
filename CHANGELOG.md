# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-09-02

### Added
- Security scanning and tag-driven release-readiness validation for Go, Helm, and container builds.

### Fixed
- Helm chart, operator deployment, examples, and documentation now consistently reference version 0.5.0.
- Vulnerable `golang.org/x/net` and `golang.org/x/text` dependency versions were upgraded to fixed releases.
- GolangCI-Lint configuration now uses the supported v2 schema and validates with golangci-lint 2.12.2.
- Replica calculations remain in `int64` until bounded, preventing overflow before conversion to Kubernetes `int32` replica counts.

## [0.4.0] - 2026-04-20

### Added
- Project governance files (`CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`).

## [0.1.0-alpha.1] - 2026-02-14

### Added
- Initial Kubernetes operator with `OJSCluster` custom resource, multi-backend support, auto-scaling, Prometheus monitoring, and leader election.
