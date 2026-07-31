# Rancher API Server Extension

A repository designed to test, build, and extend custom API server components using the `rancher/apiserver` framework, schemas, and store patterns.

## Overview

This project provides a minimal implementation utilizing Rancher's apiserver framework. It serves as a foundation for exploring:
- Custom schema definitions and API routing.
- Store implementations for handling CRUD operations and resource transformations.
- Integrating request parsing and HTTP handlers built on top of `rancher/apiserver`.

## Project Structure

```text
.
├── cmd/               # Entrypoints and main application binaries
├── pkg/               # Core logic, custom schemas, stores, and server wiring
├── Makefile           # Build, test, and utility automation tasks
├── go.mod             # Go modules and dependency tracking
└── go.sum             # Dependency checksums
```

## Prerequisites

- [Go](https://golang.org/) (matching the version specified in `go.mod`)
- [Make](https://www.gnu.org/software/make/)

## Getting Started

### 1. Clone the Repository

```bash
git clone https://github.com/rancher/apiserver-tests.git
cd apiserver-tests
```

### 2. Tidy Dependencies

Ensure all Go modules are resolved correctly:

```bash
go mod tidy
```

### 3. Build the Server

Compile the binary from the `cmd/` directory:

```bash
go build -o bin/apiserver ./cmd/...
```

### 4. Run the Server

Start the custom apiserver locally:

```bash
./bin/apiserver
```

## Running Tests

To run the test suite locally, execute the following command from the repository root:

```bash
make test
```