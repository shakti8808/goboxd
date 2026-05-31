# Requirements Document

## Introduction

GoboxD is a Go HTTP service that compiles and executes untrusted code inside isolated NsJail sandboxes and returns results. This spec defines the **architecture and design** of GoboxD, not its implementation. The deliverable for this feature is a design document covering folder structure, package design, data flow, request lifecycle, and implementation phases scoped to `cmd/goboxd`, `internal`, `docs`, and `tests`.

The design must support an HTTP API (`POST /run`, `GET /healthz`, `GET /readyz`, `GET /info`), execution of Python3 and C++ code, a YAML-driven language registry, a bounded worker pool, NsJail integration, security validation, and metrics. Infrastructure (`Dockerfile`, `docker-compose.yml`, `Makefile`) is assumed provided and out of scope for modification, and this spec does not produce executable code.

## Glossary

- **GoboxD**: The Go HTTP service that executes untrusted code in sandboxes.
- **Architecture_Document**: The design artifact produced by this feature, located under `docs/`.
- **API_Server**: The component exposing the HTTP endpoints (`/run`, `/healthz`, `/readyz`, `/info`).
- **Run_Handler**: The component that processes `POST /run` requests.
- **Health_Handler**: The component that processes `GET /healthz` and `GET /readyz` requests.
- **Info_Handler**: The component that processes `GET /info` requests.
- **Language_Registry**: The component that loads and exposes per-language execution definitions from a YAML file.
- **Language_Definition**: A single entry in the Language_Registry describing source filename, compile command, run command, and default resource limits for one language.
- **Worker_Pool**: The bounded-concurrency component that dispatches submitted code executions to a fixed number of workers with a request queue.
- **Sandbox_Runner**: The component that builds an NsJail invocation and executes a compile or run step inside the sandbox.
- **NsJail**: The external binary (`/usr/local/bin/nsjail`) that provides Linux namespace, cgroup, rlimit, and seccomp isolation.
- **Security_Validator**: The component that validates incoming `Code_Submission` payloads against size, language, and policy rules before execution.
- **Metrics_Collector**: The component that records and exposes runtime metrics (request counts, durations, queue depth, sandbox outcomes).
- **Code_Submission**: The JSON request body sent to `POST /run` containing language identifier, source code, optional stdin, and optional resource overrides.
- **Execution_Result**: The JSON response body returned from `POST /run` containing status, stdout, stderr, exit code, and timing information.
- **Sandbox_Job_Directory**: The per-request working directory created on the host filesystem and bind-mounted into the NsJail sandbox.
- **Resource_Limits**: The per-job bounds for wall time, memory, and process count enforced by NsJail.
- **Implementation_Phases**: The ordered set of milestones in the Architecture_Document that sequence delivery of the design.

## Requirements

### Requirement 1: Architecture Document Deliverable

**User Story:** As a maintainer of GoboxD, I want a single design document that captures the application architecture, so that contributors can implement the service from an agreed blueprint.

#### Acceptance Criteria

1. THE Architecture_Document SHALL be produced as a single Markdown file located at `docs/architecture.md` within the GoboxD repository.
2. THE Architecture_Document SHALL contain a section titled to indicate folder structure that lists each of `cmd/goboxd`, `internal`, `docs`, and `tests` and states the purpose and intended contents of each listed folder.
3. THE Architecture_Document SHALL contain a section titled to indicate package design that, for every package under `internal/`, states the package responsibility, its exported types and functions, and its dependencies on other packages within `internal/`.
4. THE Architecture_Document SHALL contain a section titled to indicate data flow that presents an ordered sequence of steps from HTTP request entry to HTTP response exit and identifies the package or component responsible for each step.
5. THE Architecture_Document SHALL contain a section titled to indicate the request lifecycle for `POST /run` that presents an ordered sequence of stages covering request receipt, input validation, execution, response emission, and resource cleanup, with the responsible package or component identified for each stage.
6. THE Architecture_Document SHALL contain a section titled to indicate Implementation_Phases that presents an ordered list of at least three milestones, where each milestone states its scope, its deliverables, and the completion criteria used to determine that the milestone is finished.
7. THE Architecture_Document SHALL contain a scope section that explicitly states the design scope is limited to `cmd/goboxd`, `internal`, `docs`, and `tests` and that paths outside this list are out of scope.
8. IF the Architecture_Document is missing any of the sections required by criteria 2 through 7, THEN THE Architecture_Document SHALL be treated as not satisfying this requirement.

### Requirement 2: Non-Goals and Infrastructure Boundaries

**User Story:** As a project owner, I want explicit non-goals captured in the design, so that contributors do not modify infrastructure files or introduce premature implementation.

#### Acceptance Criteria

1. THE Architecture_Document SHALL declare that the files `Dockerfile`, `docker-compose.yml`, and `Makefile` SHALL NOT be created, modified, renamed, or deleted as part of implementing this feature, and SHALL list these three filenames verbatim in a dedicated "Non-Goals" section.
2. THE Architecture_Document SHALL declare that the following infrastructure capabilities are pre-existing prerequisites supplied by the host environment and SHALL NOT be implemented, configured, or altered by this feature: (a) container image build, (b) container runtime, (c) NsJail binary availability on the runtime path, and (d) network port exposure.
3. THE Architecture_Document SHALL declare that no files with the `.go` extension SHALL be created, modified, or deleted as part of producing this feature, and SHALL state that the deliverable consists solely of design documentation.
4. THE Architecture_Document SHALL describe each component using exactly three labeled subsections titled "Responsibilities", "Interfaces", and "Interactions", and SHALL NOT contain Go source code, Go package declarations, Go type or struct definitions, or Go function bodies.
5. IF the Architecture_Document is missing the "Non-Goals" section, omits any of the three filenames in criterion 1, omits any of the four infrastructure items in criterion 2, or contains Go source code as defined in criterion 4, THEN THE review process SHALL reject the document with a rejection notice identifying each missing or violating item.

### Requirement 3: HTTP API Surface

**User Story:** As an API consumer, I want a documented set of HTTP endpoints, so that I can integrate with GoboxD.

#### Acceptance Criteria

1. WHEN a `POST /run` request is received with a valid request body, THE API_Server SHALL invoke the Run_Handler and return HTTP 200 with the execution result on the success path.
2. WHEN a `GET /healthz` request is received, THE API_Server SHALL return HTTP 200 if the process is live, without invoking the Run_Handler or any sandbox subsystem.
3. WHEN a `GET /readyz` request is received, THE API_Server SHALL return HTTP 200 if the service is ready to accept `POST /run` traffic, and SHALL return HTTP 503 with a response body indicating the not-ready reason otherwise.
4. WHEN a `GET /info` request is received, THE API_Server SHALL return HTTP 200 with a response body containing service metadata fields (at minimum: service name, version, and supported language identifiers).
5. THE Architecture_Document SHALL specify, for each of the four endpoints (`POST /run`, `GET /healthz`, `GET /readyz`, `GET /info`), the request shape (HTTP method, path, required headers, request body field names and types where applicable), the response shape (response body field names and types, response Content-Type), and the HTTP status codes returned for the success path and for each documented failure mode.
6. THE API_Server SHALL bind to TCP address `0.0.0.0` on port 8080 to match the runtime image's exposed port and to accept connections from outside the container.
7. WHEN a request targets a path other than `/run`, `/healthz`, `/readyz`, or `/info`, THE API_Server SHALL return HTTP 404 with a response body indicating the path is not recognised, without invoking the Run_Handler.
8. WHEN a request targets one of `/run`, `/healthz`, `/readyz`, or `/info` using an HTTP method not supported by that endpoint, THE API_Server SHALL return HTTP 405 with an `Allow` header listing the supported methods for that path, without invoking the Run_Handler.
9. THE API_Server SHALL reserve HTTP 404 exclusively for unrecognised paths and SHALL use HTTP 400 for recognised paths whose request body fails validation, HTTP 405 for unsupported methods on recognised paths, and HTTP 500 for unexpected internal failures on recognised paths.
10. IF a `POST /run` request has a Content-Type other than `application/json` or a request body that is not valid JSON conforming to the documented request shape, THEN THE API_Server SHALL return HTTP 400 with a response body indicating the validation failure, without invoking the Run_Handler.
11. THE API_Server SHALL enforce a maximum request body size of 1 MiB on `POST /run`, and IF a request body exceeds this limit, THEN THE API_Server SHALL return HTTP 400 with a response body indicating the size limit was exceeded, without invoking the Run_Handler.

### Requirement 4: Run Endpoint Behaviour

**User Story:** As an API consumer, I want to submit source code and receive its execution result, so that I can run untrusted code without provisioning my own sandbox.

#### Acceptance Criteria

1. WHEN `POST /run` receives a well-formed Code_Submission with a language identifier present in the Language_Registry, non-empty UTF-8 source code whose length does not exceed the configured maximum source size, and the Worker_Pool has at least one free queue slot, THE Run_Handler SHALL enqueue the job to the Worker_Pool, await its completion, and return the resulting Execution_Result to the caller.
2. THE Execution_Result SHALL include the terminal execution status, the captured stdout truncated to the configured stdout capture limit, the captured stderr truncated to the configured stderr capture limit, the process exit code (or a documented sentinel value when the process was terminated by signal), and the wall-clock duration of the run step expressed in milliseconds.
3. IF a Code_Submission specifies a language identifier that is not present in the Language_Registry, THEN THE Run_Handler SHALL reject the submission without enqueuing it and SHALL return HTTP 400 with an error body identifying the unknown language.
4. IF a Code_Submission fails Security_Validator checks, THEN THE Run_Handler SHALL return HTTP 400 with an error body identifying the violated rule and SHALL NOT enqueue the job.
5. IF the Worker_Pool queue is at its configured capacity when a new submission arrives, THEN THE Run_Handler SHALL reject the submission without enqueuing it and SHALL return HTTP 429 with an error body indicating the service is at capacity.
6. WHEN sandboxed execution reaches the effective wall-time Resource_Limits, THE Run_Handler SHALL terminate the sandboxed process and return an Execution_Result with status `TIME_LIMIT_EXCEEDED` and the wall-clock duration measured at the moment of termination.
7. WHEN sandboxed execution exceeds the effective memory Resource_Limits, THE Run_Handler SHALL terminate the sandboxed process and return an Execution_Result with status `MEMORY_LIMIT_EXCEEDED` if the sandbox can attribute termination to the memory limit, and otherwise with status `RUNTIME_ERROR`, applying the same selection rule deterministically across requests.
8. WHEN compilation of a compiled-language Code_Submission fails or exceeds the configured compile-step time limit, THE Run_Handler SHALL return an Execution_Result with status `COMPILATION_ERROR` and the captured compiler stderr truncated to the configured stderr capture limit, and SHALL NOT execute the run step.
9. WHEN request handling for a Code_Submission completes due to success, failure, or timeout, THE Run_Handler SHALL remove the Sandbox_Job_Directory and all of its contents before the request connection is closed.
10. IF removal of the Sandbox_Job_Directory fails after request handling completes, THEN THE Run_Handler SHALL still return the Execution_Result to the caller and SHALL record the cleanup failure for operator review.
11. IF a Code_Submission has a malformed request body, is missing a required field, or contains source code that exceeds the configured maximum source size, THEN THE Run_Handler SHALL return HTTP 400 with an error body identifying the validation failure and SHALL NOT enqueue the job.

### Requirement 5: Health and Readiness Endpoints

**User Story:** As an orchestrator (Kubernetes, Docker Compose, load balancer), I want liveness and readiness probes, so that I can route traffic only to healthy GoboxD instances.

#### Acceptance Criteria

1. WHEN `GET /healthz` is received and the GoboxD process is accepting HTTP connections, THE Health_Handler SHALL return HTTP 200 with a JSON body containing a status field set to a healthy indicator value, within 100 milliseconds under nominal load.
2. WHEN `GET /readyz` is received and the Language_Registry has completed initialization with at least one language definition loaded and zero load errors, and the Worker_Pool has reached its configured minimum worker count in an idle-or-busy state, and the NsJail binary exists and is executable at the configured path, THE Health_Handler SHALL return HTTP 200 with a JSON body containing a ready indicator value, within 200 milliseconds under nominal load.
3. IF the Language_Registry has not completed initialization or reports one or more load errors, THEN THE Health_Handler SHALL return HTTP 503 from `GET /readyz` with a JSON error body containing a field that names the failed component as the Language_Registry and a field describing the failure reason, without exposing internal stack traces.
4. IF the NsJail binary is not present at the configured path or is present but not executable by the GoboxD process, THEN THE Health_Handler SHALL return HTTP 503 from `GET /readyz` with a JSON error body containing a field that names the missing dependency as the NsJail binary and a field containing the configured path that was checked.
5. IF any single readiness condition fails, THEN THE Health_Handler SHALL return HTTP 503 from `GET /readyz` regardless of whether other readiness conditions pass, and the JSON error body SHALL enumerate every failed condition rather than only the first detected failure.
6. THE Health_Handler SHALL respond to `GET /healthz` and `GET /readyz` without enqueuing work to the Worker_Pool, without spawning NsJail subprocesses, and without acquiring locks held by code execution requests.
7. IF a request to `/healthz` or `/readyz` uses an HTTP method other than GET, THEN THE Health_Handler SHALL return HTTP 405 with an Allow header listing GET as the only permitted method.
8. WHILE the GoboxD process is in a shutdown state initiated by an OS termination signal, THE Health_Handler SHALL return HTTP 503 from `GET /readyz` so that the orchestrator stops routing new traffic, while continuing to return HTTP 200 from `GET /healthz` until the HTTP listener is closed.

### Requirement 6: Info Endpoint

**User Story:** As an operator, I want a metadata endpoint exposing service version and configuration, so that I can verify deployments and supported capabilities.

#### Acceptance Criteria

1. WHEN an HTTP GET request is received at the `/info` path, THE Info_Handler SHALL return an HTTP 200 response within 500 milliseconds with a JSON body containing the service name as a non-empty string, the build version as a non-empty string, the list of registered language identifiers as an array of strings (which MAY be empty), and the configured Worker_Pool size as a non-negative integer.
2. THE Info_Handler SHALL NOT include secrets (such as credentials, API keys, or authentication tokens), file system paths, or any value sourced from a Code_Submission in its response body.
3. THE Info_Handler SHALL respond to every request at the `/info` path without enqueuing work to the Worker_Pool.
4. IF an HTTP request received at the `/info` path uses a method other than GET, THEN THE Info_Handler SHALL return an HTTP 405 response indicating the method is not allowed and SHALL NOT enqueue work to the Worker_Pool.

### Requirement 7: YAML Language Registry

**User Story:** As a maintainer, I want languages defined in a YAML file, so that I can add or modify language support without changing Go source code.

#### Acceptance Criteria

1. WHEN GoboxD starts, THE Language_Registry SHALL load all Language_Definitions from a YAML file whose path is provided via configuration or environment variable, and SHALL complete loading before GoboxD accepts any Code_Execution_Requests.
2. THE Language_Registry SHALL expose, for each Language_Definition, the language identifier (non-empty string), the source filename (non-empty string), an optional compile command, the run command (non-empty string), and the default Resource_Limits for the compile phase and the run phase.
3. IF the YAML file is missing, unreadable, or fails to parse on startup, THEN THE GoboxD SHALL terminate with a non-zero exit code and emit a log entry identifying the configured file path and the failure reason.
4. IF a Language_Definition omits any required field (language identifier, source filename, run command, or default Resource_Limits) on startup, THEN THE GoboxD SHALL terminate with a non-zero exit code and emit a log entry identifying the missing field and the language identifier, or the position within the file when the language identifier itself is missing.
5. WHEN queried with a language identifier using case-sensitive exact match, THE Language_Registry SHALL return the matching Language_Definition, or a not-found result distinguishable from a successful response, without modifying the registry state.
6. THE Architecture_Document SHALL include a representative YAML schema example showing every supported field of a Language_Definition, covering required fields, optional fields, and the Resource_Limits structure for compile and run phases.
7. IF the YAML file contains two or more Language_Definitions with the same language identifier on startup, THEN THE GoboxD SHALL terminate with a non-zero exit code and emit a log entry identifying the duplicated language identifier.

### Requirement 8: Supported Languages

**User Story:** As an API consumer, I want Python3 and C++ supported out of the box, so that I can submit code in either language without additional configuration.

#### Acceptance Criteria

1. THE Language_Registry SHALL include a Language_Definition with identifier `py3` that populates every field required by the Language_Registry schema, including the source filename, the run command, and the default Resource_Limits for the run step covering wall time, memory, and process count.
2. THE Language_Registry SHALL include a Language_Definition with identifier `cpp` that populates every field required by the Language_Registry schema, including the source filename, the compile command, the run command, and the default Resource_Limits for both the compile step and the run step covering wall time, memory, and process count.
3. THE Language_Definition for `py3` SHALL specify a non-empty run command, SHALL omit the compile command, and THE Language_Registry SHALL treat the absent compile command as an instruction to skip the compile step for any `py3` Code_Submission.
4. THE Language_Definition for `cpp` SHALL specify a non-empty compile command that produces a compiled artifact within the Sandbox_Job_Directory and a non-empty run command that executes that artifact from the Sandbox_Job_Directory.
5. THE Architecture_Document SHALL describe the procedure for adding a third language by enumerating every YAML field a maintainer must populate in a new Language_Definition entry, providing one worked example entry that demonstrates each enumerated field, and stating that no Go source files under `internal/` are required to change for the new language to be loaded by the Language_Registry.

### Requirement 9: Worker Pool Concurrency

**User Story:** As an operator, I want bounded concurrency, so that GoboxD does not exhaust host resources under load.

#### Acceptance Criteria

1. THE Worker_Pool SHALL execute at most a configured maximum number of concurrent jobs, where the maximum is a positive integer between 1 and 1024 inclusive.
2. THE Worker_Pool SHALL accept additional jobs into a bounded FIFO queue up to a configured maximum queue length between 0 and 10000 inclusive.
3. WHEN a submission arrives AND both worker capacity and queue capacity are exhausted, THE Worker_Pool SHALL reject the submission immediately and return a rejection error to the caller indicating capacity exhaustion, even if a worker becomes idle moments later.
4. WHEN a worker becomes idle AND the queue is non-empty, THE Worker_Pool SHALL dequeue the next job in FIFO order and assign it to the idle worker.
5. THE Worker_Pool SHALL be configurable via environment variables for maximum concurrent jobs, maximum queue length, and drain timeout, as documented in the Architecture_Document.
6. IF any Worker_Pool configuration value is missing, non-numeric, or outside its permitted range, THEN THE Worker_Pool SHALL fail to start and return a configuration error indicating which setting is invalid.
7. WHEN GoboxD receives a shutdown signal AND the configured drain timeout is greater than zero seconds and less than or equal to 3600 seconds, THE Worker_Pool SHALL stop accepting new submissions, wait for in-flight jobs to complete up to the configured drain timeout, and then return.
8. IF the drain timeout elapses before all in-flight jobs complete, THEN THE Worker_Pool SHALL cancel remaining in-flight jobs, discard queued jobs, and proceed to shutdown.
9. WHEN GoboxD receives a shutdown signal AND the configured drain timeout is zero seconds, THE Worker_Pool SHALL stop accepting new submissions, immediately cancel all in-flight jobs, discard queued jobs, and proceed to shutdown.

### Requirement 10: NsJail Integration

**User Story:** As a security engineer, I want every untrusted process executed inside NsJail with strong isolation, so that user code cannot escape the sandbox.

#### Acceptance Criteria

1. WHEN the Sandbox_Runner receives a compile step or run step request, THE Sandbox_Runner SHALL invoke the NsJail binary as a child subprocess and SHALL NOT execute compiler or interpreter binaries directly outside NsJail.
2. WHEN the Sandbox_Runner invokes NsJail, THE Sandbox_Runner SHALL pass flags that enable user namespace, PID namespace, mount namespace, network namespace, IPC namespace, and UTS namespace isolation, and SHALL pass flags that disable network access for the sandboxed process.
3. WHEN the Sandbox_Runner invokes NsJail, THE Sandbox_Runner SHALL pass flags that enforce the effective Resource_Limits, including wall-clock time limit in seconds (1 to 60), CPU time limit in seconds (1 to 60), memory limit in megabytes (16 to 1024), maximum process/thread count (1 to 64), and maximum output file size in megabytes (1 to 64).
4. WHEN the Sandbox_Runner invokes NsJail, THE Sandbox_Runner SHALL bind-mount the Sandbox_Job_Directory as the sandboxed working directory in read-write mode and SHALL bind-mount all other host paths required by the language runtime in read-only mode.
5. WHEN the Code_Submission includes optional stdin, THE Sandbox_Runner SHALL feed up to 1 megabyte of stdin to the sandboxed process and SHALL capture up to 1 megabyte each of the sandboxed process's stdout and stderr, truncating any bytes beyond these limits and indicating truncation in the Execution_Result.
6. WHEN NsJail terminates the sandboxed process, THE Sandbox_Runner SHALL classify the outcome into exactly one Execution_Result status using the following deterministic mapping: exit code 0 maps to `OK`; non-zero exit code from a compile step maps to `COMPILATION_ERROR`; non-zero exit code or termination by signal from a run step maps to `RUNTIME_ERROR`; NsJail log output indicating wall-time, CPU-time, or memory limit violation maps to `TIME_LIMIT_EXCEEDED`; and any NsJail invocation failure or unparseable outcome maps to `INTERNAL_ERROR`.
7. IF the NsJail binary cannot be located on the configured path, fails to launch within 5 seconds, or exits before producing a parseable outcome, THEN THE Sandbox_Runner SHALL return an Execution_Result with status `INTERNAL_ERROR` and a non-empty error indicator describing the failure category, SHALL log the failure with the request identifier, and SHALL NOT return partial stdout or stderr from the sandboxed process.
8. THE Architecture_Document SHALL document the complete NsJail argument template, identify every placeholder substituted per request, and specify the source field and validation rule for each placeholder.

### Requirement 11: Security Validation

**User Story:** As a security engineer, I want incoming submissions validated before any sandbox is started, so that obviously malformed or oversized payloads are rejected cheaply.

#### Acceptance Criteria

1. IF a Code_Submission's source code size in bytes exceeds the configured maximum (default 1,048,576 bytes, range 1 to 10,485,760 bytes), THEN THE Security_Validator SHALL reject the submission and return a structured rejection reason with rule name "source_size_exceeded" and the configured limit value.
2. IF a Code_Submission's stdin payload size in bytes exceeds the configured maximum (default 1,048,576 bytes, range 0 to 10,485,760 bytes), THEN THE Security_Validator SHALL reject the submission and return a structured rejection reason with rule name "stdin_size_exceeded" and the configured limit value.
3. IF a Code_Submission's language identifier is not present in the Language_Registry, THEN THE Security_Validator SHALL reject the submission and return a structured rejection reason with rule name "language_not_registered" and the submitted language identifier.
4. IF a Code_Submission's Resource_Limits override exceeds the configured per-language maximum for that limit, THEN THE Security_Validator SHALL reject the submission and return a structured rejection reason with rule name "resource_limit_exceeded", the violated limit name, the requested value, and the configured maximum.
5. IF a Code_Submission's source code size, stdin payload size, language identifier, or Resource_Limits field is absent or unparseable, THEN THE Security_Validator SHALL reject the submission and return a structured rejection reason with rule name "malformed_submission" and the offending field name.
6. WHEN a Code_Submission is rejected, THE Security_Validator SHALL return the rejection reason to the caller without enqueuing the submission to the Worker_Pool and without allocating a sandbox.
7. WHEN a Code_Submission is received, THE Security_Validator SHALL complete all validation rules and produce an accept or reject decision within 50 milliseconds at the 99th percentile before the Worker_Pool enqueue step is invoked.
8. THE Architecture_Document SHALL list every Security_Validator rule, the configuration key that controls its bound, the default value, and the allowed range for that configuration key.

### Requirement 12: Metrics

**User Story:** As an operator, I want runtime metrics emitted in a standard format, so that I can monitor throughput, latency, and failure rates.

#### Acceptance Criteria

1. THE Metrics_Collector SHALL record a monotonically increasing counter of received `POST /run` requests, partitioned by a language identifier label whose value is one of the supported language identifiers defined in the Architecture_Document and by an Execution_Result status label whose value is one of the enumerated statuses defined in the Architecture_Document (for example success, compile_error, runtime_error, timeout, rejected).
2. THE Metrics_Collector SHALL record the wall-clock duration of `POST /run` requests as a histogram measured in seconds, with explicit bucket boundaries defined in the Architecture_Document covering at least the range 0.01 seconds to 60 seconds.
3. WHEN a job is enqueued to or dequeued from the Worker_Pool, THE Metrics_Collector SHALL update a gauge reflecting the current Worker_Pool queue depth as a non-negative integer in the range 0 to the configured maximum queue size.
4. THE Metrics_Collector SHALL record a monotonically increasing counter of Security_Validator rejections, partitioned by a rule name label whose value is one of the rule identifiers enumerated in the Architecture_Document.
5. IF a metric value cannot be recorded because the Metrics_Collector is unavailable, THEN THE System SHALL continue processing the originating request without failure and SHALL surface an indication of the metrics failure through the operator-observable error channel defined in the Architecture_Document.
6. THE Architecture_Document SHALL specify the metrics exposition format (for example Prometheus text format), the endpoint or mechanism by which metrics are scraped, and the maximum staleness in seconds between metric update and exposition.
7. THE Architecture_Document SHALL list every metric by name, metric type (counter, gauge, or histogram), unit of measure, complete set of label keys, and the enumerated set of allowed values for each label key.

### Requirement 13: Configuration

**User Story:** As an operator, I want all tunable behaviour expressed in configuration, so that I can change behaviour without rebuilding the binary.

#### Acceptance Criteria

1. THE Architecture_Document SHALL list every configuration key consumed by GoboxD, including the key name, the source from which it is read (environment variable name and/or YAML path), the data type, the valid range or accepted values, the unit of measurement where applicable, and the default value.
2. THE Architecture_Document SHALL specify the precedence rule applied when a configuration key is set in both an environment variable and the YAML file, such that two operators reading the document would resolve the same effective value for any given key/source combination.
3. THE Architecture_Document SHALL specify the Language_Registry YAML file path as a configurable value with a documented default path and a documented data type (filesystem path string).
4. THE Architecture_Document SHALL specify the Worker_Pool size, queue length, and drain timeout as configurable values with documented defaults, documented valid ranges (minimum and maximum), and documented units of measurement (count for size and queue length, seconds for drain timeout).
5. THE Architecture_Document SHALL specify the maximum source size, maximum stdin size, and per-language Resource_Limits ceilings as configurable values with documented defaults, documented valid ranges (minimum and maximum), and documented units of measurement (bytes for source and stdin sizes; documented unit per Resource_Limits ceiling).
6. IF GoboxD starts with a configuration value that is missing for a required key, outside its documented valid range, or unparseable for its documented data type, THEN THE GoboxD SHALL terminate startup before accepting any request, exit with a non-zero exit code, and emit a log entry identifying the offending key name and the reason for rejection.
7. IF the Language_Registry YAML file referenced by configuration cannot be opened or parsed at startup, THEN THE GoboxD SHALL terminate startup before accepting any request, exit with a non-zero exit code, and emit a log entry identifying the file path and the failure reason.

### Requirement 14: Observability and Logging

**User Story:** As an operator, I want structured logs for every request and lifecycle event, so that I can diagnose failures in production.

#### Acceptance Criteria

1. WHEN a `POST /run` request completes, THE GoboxD SHALL emit a single structured log entry that includes a UUID v4 request identifier, the language identifier, the Execution_Result status, the wall-clock duration in milliseconds as an integer between 0 and 2,147,483,647, and an ISO 8601 UTC timestamp with millisecond precision.
2. WHEN the GoboxD process completes startup initialization, THE GoboxD SHALL emit exactly one structured log entry at the INFO level that includes the list of loaded language identifiers and the Worker_Pool configuration values for maximum worker count, queue capacity, and default per-job timeout in seconds.
3. WHEN the GoboxD process begins shutdown, THE GoboxD SHALL emit exactly one structured log entry at the INFO level that includes the count of jobs drained as a non-negative integer and the count of jobs cancelled as a non-negative integer.
4. WHILE the configured log level is INFO or higher, THE GoboxD SHALL omit Code_Submission source code, stdin, stdout, and stderr from every emitted log entry.
5. WHERE the configured log level is DEBUG, THE GoboxD SHALL emit additional diagnostic log entries that MAY include sandbox argument lists but SHALL omit Code_Submission source code, stdin, stdout, and stderr.
6. IF emission of a log entry fails, THEN THE GoboxD SHALL continue processing the originating request or lifecycle event without altering its Execution_Result status and SHALL increment an internal dropped-log counter exposed through the existing metrics surface.
7. IF a `POST /run` request is rejected before reaching the Worker_Pool, THEN THE GoboxD SHALL emit a structured log entry that includes the request identifier, the rejection reason category, and an ISO 8601 UTC timestamp with millisecond precision.

### Requirement 15: Folder Structure Deliverable

**User Story:** As a contributor, I want the design to specify the project's folder layout, so that I know where each component lives before writing code.

#### Acceptance Criteria

1. THE Architecture_Document SHALL contain a "Folder Structure" section that presents the project layout as a directory tree showing each top-level folder and its immediate subdirectories relative to the repository root.
2. THE Architecture_Document SHALL list `cmd/goboxd/` in the Folder Structure section, identify it as the binary entry point package, and include a one-to-three sentence description of its responsibilities.
3. THE Architecture_Document SHALL enumerate every immediate subdirectory of `internal/` and SHALL provide a one-to-three sentence responsibility description for each subdirectory.
4. THE Architecture_Document SHALL enumerate every file located directly under `docs/` by filename and SHALL provide a one-sentence description of each file's purpose.
5. THE Architecture_Document SHALL describe the structure of `tests/` and SHALL define an explicit, non-overlapping rule (by subdirectory path or by filename suffix) that uniquely classifies each test file as either a unit test or an integration test.
6. THE Architecture_Document SHALL include a mapping (table or annotated list) in which every component named in the Package Design section appears with exactly one corresponding folder path from the Folder Structure section.
7. IF a component named in the Package Design section has no corresponding folder path in the Folder Structure section, OR a folder path listed in the Folder Structure section has no corresponding component in the Package Design section, THEN THE Architecture_Document SHALL be considered incomplete and SHALL fail the Folder Structure review.

### Requirement 16: Package Design Deliverable

**User Story:** As a contributor, I want each internal package's responsibility, exported surface, and dependencies described, so that I can implement and test packages independently.

#### Acceptance Criteria

1. THE Architecture_Document SHALL describe, for every package under `internal/`, the package's single responsibility as a statement of 1 to 3 sentences that names the inputs the package accepts, the outputs it produces, and the concern it owns.
2. THE Architecture_Document SHALL list, for every package under `internal/`, every exported identifier (types, interfaces, functions, methods, constants, and variables) together with its signature or type and a one-line description of its purpose.
3. IF a package under `internal/` exposes no exported identifiers, THEN THE Architecture_Document SHALL explicitly state that the package has no exported surface and is consumed only via its `main` or `init` behavior.
4. THE Architecture_Document SHALL list, for every package under `internal/`, the complete set of other `internal/` packages it imports, and SHALL state "no internal dependencies" for any package that imports none.
5. THE Architecture_Document SHALL present the dependency graph between `internal/` packages in a form (diagram or adjacency list) where every node and every edge from criterion 4 is reproduced, and SHALL state that the graph contains zero cycles.
6. IF any cycle exists in the dependency graph between `internal/` packages, THEN THE Architecture_Document SHALL identify each cycle by listing the packages involved and SHALL describe the refactoring required to break it before implementation begins.
7. THE Architecture_Document SHALL classify every package under `internal/` into exactly one of three categories: covered by unit tests, covered only by integration tests, or not covered by automated tests, and SHALL name the test package or test suite that provides coverage for each of the first two categories.

### Requirement 17: Data Flow Deliverable

**User Story:** As a contributor, I want a single diagram or narrative that traces data from HTTP request to HTTP response, so that I can reason about end-to-end behaviour.

#### Acceptance Criteria

1. THE Architecture_Document SHALL contain exactly one data flow deliverable, presented as either a diagram or a numbered narrative, that traces a `POST /run` request as an ordered sequence of named components from the moment the HTTP request is accepted until the HTTP response is returned to the caller.
2. THE Architecture_Document SHALL reference each component in the data flow by a name that is defined elsewhere in the Architecture_Document, and SHALL NOT introduce component names that are not defined in the document.
3. THE Architecture_Document SHALL specify, for each step in the data flow, the input data received by the component, the output data produced by the component, and any persistent state written or transient in-memory state held during the step, where each of these three fields is either listed explicitly or marked as "none".
4. THE Architecture_Document SHALL specify, for each step in the data flow, every failure mode that terminates the flow at that step and the HTTP status code returned to the caller for each such failure, where each step either lists at least one failure mode with its status code or is marked as "no failure mode terminates the flow at this step".
5. THE Architecture_Document SHALL describe the happy path that produces a successful HTTP response and SHALL describe at least one error path that produces a non-success HTTP response, using the same step structure for both.
6. THE Architecture_Document SHALL show, at each step where Metrics_Collector or logging is invoked, that the invocation occurs as a side effect that does not modify the input data, the output data, or the persistent state recorded for that step.

### Requirement 18: Request Lifecycle Deliverable

**User Story:** As a contributor, I want a step-by-step request lifecycle, so that I understand sandbox setup, execution, and cleanup ordering.

#### Acceptance Criteria

1. THE Architecture_Document SHALL describe the request lifecycle for `POST /run` as a numbered, ordered sequence of steps from request receipt to response emission, with each step specifying its inputs, outputs, and the immediately preceding and following steps.
2. THE Architecture_Document SHALL include, in execution order, the Security_Validator step, the Worker_Pool enqueue step, the Sandbox_Job_Directory creation step, the compile step, the run step, the result aggregation step, and the cleanup step, and SHALL state for each step whether it is mandatory or conditional.
3. WHERE a lifecycle step is conditional, THE Architecture_Document SHALL specify the exact condition under which the step is executed and the exact condition under which the step is skipped, including the compile step being executed only for languages that require compilation.
4. THE Architecture_Document SHALL specify, for each lifecycle step, exactly one component that is responsible for executing the step, where the component name matches a component defined in the Glossary.
5. THE Architecture_Document SHALL specify the cleanup guarantees for the `POST /run` lifecycle in the presence of execution timeouts, process panics, and shutdown signals, and SHALL state, for each of these three failure modes, which lifecycle steps are guaranteed to execute, the order in which they execute, and the observable post-conditions on the Sandbox_Job_Directory and any spawned processes.
6. IF a cleanup step fails to complete for the `POST /run` lifecycle, THEN THE Architecture_Document SHALL specify the resulting system state, the error indication returned to the caller, and the recovery behavior on subsequent requests.
7. THE Architecture_Document SHALL describe the lifecycle for `GET /healthz`, `GET /readyz`, and `GET /info` separately from the `POST /run` lifecycle as ordered steps, and SHALL state for each of these three endpoints that no Sandbox_Job_Directory is created, no Worker_Pool enqueue occurs, and no compile or run step is executed.

### Requirement 19: Implementation Phases Deliverable

**User Story:** As a project owner, I want the work sequenced into phases, so that contributors can deliver incrementally and demonstrate progress.

#### Acceptance Criteria

1. THE Architecture_Document SHALL define Implementation_Phases as an ordered list containing at least three and at most six phases, where each phase is identified by a unique sequence number starting at 1 and incrementing by 1.
2. THE Architecture_Document SHALL describe, for each phase, (a) the scope as an enumerated list of named components delivered in that phase, (b) the demonstrable outcome as a list of observable behaviors each expressed as an executable command or HTTP request together with its expected response, and (c) the completion gate as a minimum statement test coverage percentage between 0 and 100 inclusive and a list of named test suites that must pass before the phase is marked complete.
3. THE Architecture_Document SHALL specify that Phase 1 delivers a runnable HTTP skeleton, where runnable is defined as a process that starts without error and returns HTTP 200 within 1 second to GET requests for `/healthz`, `/readyz`, and `/info`, and SHALL specify that Phase 1 contains no sandbox execution capability.
4. THE Architecture_Document SHALL specify that Phase 2 delivers the `POST /run` endpoint integrated with NsJail and supporting exactly one language drawn from the supported language list defined elsewhere in the requirements.
5. THE Architecture_Document SHALL specify that Phase 3 delivers the Metrics_Collector component and adds support for a second language drawn from the supported language list defined elsewhere in the requirements, distinct from the language delivered in Phase 2.
6. THE Architecture_Document SHALL identify, for each phase numbered N greater than 1, the explicit list of prior phase numbers and named deliverables from those phases that must be completed before phase N may begin.

### Requirement 20: Testing Strategy

**User Story:** As a contributor, I want the tests directory's role described in the design, so that I know what to place under `tests/` versus alongside packages.

#### Acceptance Criteria

1. THE Architecture_Document SHALL specify that unit tests live alongside the package under test inside `internal/`, in files named `*_test.go` co-located with the source files they cover.
2. THE Architecture_Document SHALL specify that integration tests, including any tests that invoke NsJail, live under `tests/` and SHALL NOT be placed under `internal/`.
3. THE Architecture_Document SHALL specify exactly one mechanism (either a Go build tag or a filename suffix convention) used to distinguish integration tests from unit tests, and SHALL state that this mechanism is what the existing `make integration` target selects.
4. THE Architecture_Document SHALL describe at least one integration test scenario for each language listed as supported in the requirements document, and each such scenario SHALL exercise `POST /run` end-to-end from HTTP request to HTTP response with an assertion on the returned execution result.
5. THE Architecture_Document SHALL describe at least one integration test scenario for each of the five Run_Handler failure modes (unknown language, oversize submission, queue full, time limit exceeded, compilation error), and each scenario SHALL state the input condition that triggers the failure and the observable failure indication returned to the caller.
6. IF a documented integration test scenario depends on NsJail being available, THEN THE Architecture_Document SHALL state the precondition (NsJail installed and executable on the test host) and the expected skip or fail behavior when that precondition is not met.

### Requirement 21: Filename and Path Safety in Language_Definitions

**User Story:** As a security engineer, I want every Language_Definition filename validated as a simple basename, so that hostile or malformed Language_Definitions cannot escape the Sandbox_Job_Directory or write to unintended host paths.

#### Acceptance Criteria

1. WHEN the Language_Registry loads a Language_Definition at startup, THE Language_Registry SHALL validate that Language_Definition.source_filename and Language_Definition.binary_filename are simple basenames containing no path separator (`/` or `\`), no parent-directory traversal sequence (`..`), no absolute path prefix, no control character whose codepoint is below 0x20, no null byte (0x00), and no platform-specific path-escape sequence.
2. IF a Language_Definition.source_filename or Language_Definition.binary_filename fails the basename validation rules during Language_Registry startup, THEN THE GoboxD SHALL terminate startup with a non-zero exit code and emit a structured log entry containing the offending field name, the language identifier, and the rejection reason.
3. WHEN the Sandbox_Runner materialises files inside the Sandbox_Job_Directory for a Code_Submission, THE Sandbox_Runner SHALL re-apply the basename validation rules to Language_Definition.source_filename and Language_Definition.binary_filename before any filesystem write occurs inside the Sandbox_Job_Directory.
4. IF the Sandbox_Runner detects a basename validation failure at request time, THEN THE Sandbox_Runner SHALL return an Execution_Result with status `INTERNAL_ERROR`, SHALL NOT execute the compile step or the run step, SHALL emit a structured log entry containing the offending field name, the language identifier, and the rejection reason, and SHALL still remove the Sandbox_Job_Directory and its contents.
5. THE Architecture_Document SHALL list the exact basename validation rules required by criterion 1 and SHALL identify the single canonical validation function location that is invoked from both the Language_Registry startup path and the Sandbox_Runner request path.

### Requirement 22: Command Template and Argument Safety

**User Story:** As a security engineer, I want compile and run command templates and arguments fixed at Language_Registry load time, so that no Code_Submission can influence executable paths, flags, environment variables, or shell behaviour.

#### Acceptance Criteria

1. THE Language_Registry SHALL treat `compile.command`, `compile.args`, `run.command`, and `run.args` from each Language_Definition as trusted templates loaded at startup from the Language_Registry YAML file, and SHALL NOT permit modification of these templates after Language_Registry load completes.
2. THE Run_Handler and the Sandbox_Runner SHALL NOT permit any field of a Code_Submission to influence (a) executable paths invoked by the Sandbox_Runner, (b) compiler or runtime flags, (c) any element of the command argv, (d) environment variables passed to the sandboxed process, or (e) shell fragments, command substitution constructs, pipes, or redirections.
3. WHEN the Sandbox_Runner expands placeholders inside `compile.args` or `run.args`, THE Sandbox_Runner SHALL substitute only placeholder names listed in a documented closed allowlist whose members include `{{SOURCE_FILENAME}}`, `{{BINARY_FILENAME}}`, and `{{STDIN_FILE}}`.
4. IF a Language_Definition's `compile.args` or `run.args` contains a placeholder name that is not present in the documented allowlist, THEN THE GoboxD SHALL terminate startup with a non-zero exit code and emit a structured log entry containing the language identifier, the offending args entry, and the unknown placeholder name.
5. IF the Sandbox_Runner detects a placeholder name not present in the documented allowlist at request time, THEN THE Sandbox_Runner SHALL abort the Code_Submission, return an Execution_Result with status `INTERNAL_ERROR`, and emit a structured log entry containing the language identifier, the offending args entry, and the unknown placeholder name, without invoking the compile step or the run step.
6. WHEN the Sandbox_Runner invokes the compile step or the run step, THE Sandbox_Runner SHALL launch the command using argv (execve-style) invocation and SHALL NOT execute the command through a shell interpreter or by concatenating arguments into a single command string.
7. THE Architecture_Document SHALL document the closed placeholder allowlist required by criterion 3 and the substitution algorithm applied to `compile.args` and `run.args`.

### Requirement 23: Streaming Output Protection

**User Story:** As an operator, I want stdout and stderr capture bounded during streaming, so that a sandboxed process cannot exhaust GoboxD memory by emitting unbounded output.

#### Acceptance Criteria

1. WHILE the sandboxed process is running, THE Sandbox_Runner SHALL enforce the stdout and stderr capture limits during streaming and SHALL NOT buffer the full stream in memory before truncating.
2. WHEN the cumulative captured stdout byte count reaches `STDOUT_CAPTURE_LIMIT_BYTES`, THE Sandbox_Runner SHALL discard subsequent stdout bytes as they arrive without retaining them in memory.
3. WHEN the cumulative captured stderr byte count reaches `STDERR_CAPTURE_LIMIT_BYTES`, THE Sandbox_Runner SHALL discard subsequent stderr bytes as they arrive without retaining them in memory.
4. THE memory used by the Sandbox_Runner for stdout and stderr capture SHALL be bounded by `STDOUT_CAPTURE_LIMIT_BYTES` plus `STDERR_CAPTURE_LIMIT_BYTES` plus a constant overhead, regardless of the total volume of stdout or stderr emitted by the sandboxed process.
5. WHEN the sandboxed process emits more stdout bytes than `STDOUT_CAPTURE_LIMIT_BYTES`, THE Sandbox_Runner SHALL set the Execution_Result field `stdout_truncated` to true.
6. WHEN the sandboxed process emits more stderr bytes than `STDERR_CAPTURE_LIMIT_BYTES`, THE Sandbox_Runner SHALL set the Execution_Result field `stderr_truncated` to true.
7. THE Architecture_Document SHALL document the streaming-discard mechanism required by criteria 1 through 3, the constant overhead bound from criterion 4, and the relationship between the cumulative byte counters and the `stdout_truncated` and `stderr_truncated` flags on Execution_Result.

### Requirement 24: Workspace Isolation

**User Story:** As a security engineer, I want every request to use an exclusive Sandbox_Job_Directory, so that requests cannot read, modify, or interfere with each other's workspaces.

#### Acceptance Criteria

1. WHEN the Sandbox_Runner creates a Sandbox_Job_Directory for a Code_Submission, THE Sandbox_Runner SHALL name the directory using a non-guessable identifier produced by a cryptographically secure source, where the identifier is either a UUID v4 or at least 128 bits of cryptographically random data.
2. THE Sandbox_Runner SHALL ensure that no two requests, whether concurrent or sequential, share a Sandbox_Job_Directory path or reuse the contents of a previously used Sandbox_Job_Directory.
3. THE Sandbox_Runner SHALL ensure that each Sandbox_Job_Directory is owned exclusively by a single request lifecycle from the moment of creation through the cleanup step.
4. WHEN a Code_Submission lifecycle terminates due to any of the following conditions — success, compile failure, runtime failure, `INTERNAL_ERROR`, execution timeout, runtime panic, shutdown signal, or request cancellation — THE Sandbox_Runner SHALL remove the Sandbox_Job_Directory and all of its contents.
5. IF the Sandbox_Runner is asked to operate on a Sandbox_Job_Directory that it did not itself create for the current request, THEN THE Sandbox_Runner SHALL refuse the operation, return an Execution_Result with status `INTERNAL_ERROR`, and emit a structured log entry containing the offending Sandbox_Job_Directory path and the request identifier.
6. THE Architecture_Document SHALL document the per-request Sandbox_Job_Directory naming scheme required by criterion 1 and the ownership invariant required by criterion 3.

### Requirement 25: Startup-Time Sandbox Prerequisite Validation

**User Story:** As an operator, I want sandbox prerequisites validated before the HTTP listener accepts traffic, so that GoboxD cannot serve requests with a broken sandbox configuration.

#### Acceptance Criteria

1. WHEN GoboxD starts, THE GoboxD SHALL validate, before the HTTP listener begins accepting connections, that the configured sandbox root directory exists, is owned by the GoboxD process user, and is not world-writable.
2. WHEN GoboxD starts, THE GoboxD SHALL validate, before the HTTP listener begins accepting connections, that every read-only bind-mount path referenced by Language_Definitions or by the Sandbox_Runner exists and is readable by the GoboxD process user.
3. WHEN GoboxD starts, THE GoboxD SHALL validate, before the HTTP listener begins accepting connections, that the NsJail binary at the configured path exists, is a regular file, and is executable by the GoboxD process user.
4. IF any prerequisite check defined in criteria 1 through 3 fails at startup, THEN THE GoboxD SHALL terminate with a non-zero exit code and emit a structured log entry naming the failed prerequisite, the path that was checked, and the rejection reason.
5. THE GoboxD SHALL complete the prerequisite checks defined in criteria 1 through 3 before the HTTP listener begins accepting connections, such that no `GET /healthz` or `GET /readyz` response can be observed before prerequisite validation completes.
6. THE Health_Handler SHALL NOT return HTTP 200 from `GET /healthz` or `GET /readyz` in a manner that masks a startup prerequisite failure, because startup prerequisite failures terminate the GoboxD process before the HTTP listener is opened.

### Requirement 26: Security Testing Coverage

**User Story:** As a security engineer, I want every security-related boundary covered by an explicit integration test scenario in the Architecture_Document, so that regressions in security checks are detected automatically.

#### Acceptance Criteria

1. THE Architecture_Document SHALL describe at least one integration test scenario covering path traversal sequences in Language_Definition.source_filename and Language_Definition.binary_filename, where the observable success criterion is that the Language_Registry rejects the offending Language_Definition at startup with a non-zero exit code.
2. THE Architecture_Document SHALL describe at least one integration test scenario covering invalid filename characters (control characters with codepoints below 0x20, null bytes, and path separators) in Language_Definition.source_filename and Language_Definition.binary_filename, where the observable success criterion is that the Language_Registry rejects the offending Language_Definition at startup with a non-zero exit code.
3. THE Architecture_Document SHALL describe at least one integration test scenario covering command-template injection attempts via Code_Submission fields, where the observable success criterion is that either the Security_Validator rejects the submission or the registry-bound argv passed to the sandboxed process is unaffected by the Code_Submission content.
4. THE Architecture_Document SHALL describe at least one integration test scenario covering shell metacharacter injection attempts in Code_Submission.source and Code_Submission.stdin, where the observable success criterion is that no shell interpreter is invoked and that the metacharacter payload is treated as data only.
5. THE Architecture_Document SHALL describe at least one integration test scenario covering excessive stdout generation by a sandboxed process, where the observable success criterion is that Execution_Result.stdout_truncated is set to true and that Sandbox_Runner memory remains bounded by the constant documented under Requirement 23.
6. THE Architecture_Document SHALL describe at least one integration test scenario covering excessive stderr generation by a sandboxed process, where the observable success criterion is that Execution_Result.stderr_truncated is set to true and that Sandbox_Runner memory remains bounded by the constant documented under Requirement 23.
7. THE Architecture_Document SHALL describe at least one integration test scenario covering workspace isolation, where the observable success criterion is that two concurrent Code_Submission requests cannot read each other's Sandbox_Job_Directory contents.
8. THE Architecture_Document SHALL describe at least one integration test scenario covering orphan Sandbox_Job_Directory cleanup, where the observable success criterion is that a Sandbox_Job_Directory whose age exceeds `SANDBOX_ORPHAN_TTL_S` is reaped from the sandbox root directory.
9. THE Architecture_Document SHALL state, for each scenario required by criteria 1 through 8, the input condition that triggers the scenario, the observable success criterion, and the expected metric counter increments (with their label values) and structured log entries (with their levels and field names).

### Requirement 27: Security_Validator Rule Catalog Expansion

**User Story:** As an operator, I want the Security_Validator rule catalog kept current with all newly introduced security checks, so that I can map every rejection to documented behaviour, metrics, and log entries.

#### Acceptance Criteria

1. THE Architecture_Document Security_Validator rule catalog SHALL include every newly introduced security check defined by Requirements 21 through 25.
2. THE Architecture_Document SHALL document, for each new rule added under criterion 1, the validation rule name as a stable identifier.
3. THE Architecture_Document SHALL document, for each new rule added under criterion 1, the trigger condition under which the rule fires.
4. THE Architecture_Document SHALL document, for each new rule added under criterion 1, the rejection response shape returned to the caller, including HTTP status code where applicable and the structured fields contained in the response body.
5. THE Architecture_Document SHALL document, for each new rule added under criterion 1, the metric counter name that increments when the rule fires and the complete set of label keys and label values applied to that counter.
6. THE Architecture_Document SHALL document, for each new rule added under criterion 1, the log entry that is emitted when the rule fires, the log level at which the entry is emitted, and the structured fields the entry contains.
