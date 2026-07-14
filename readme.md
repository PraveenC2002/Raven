# Raven

Raven is an autonomous infrastructure diagnosis agent that investigates Linux servers over controlled SSH access using LLM-driven reasoning and tool calling.

Given a natural-language problem, Raven independently performs an iterative investigation: it gathers evidence from the target machine, analyzes system state, logs, services, and configuration, refines its understanding based on observations, and produces a root-cause analysis without requiring a human to approve each investigation step.

Raven is designed around three core ideas: **autonomous investigation, controlled execution, and complete auditability**.

## How Raven Works

A user submits an infrastructure problem through a supported transport. Raven creates an investigation agent with information about the target machine and the reported issue.

The agent then performs an iterative investigation:

```text
Investigation Request
        │
        ▼
   Reason About Issue
        │
        ▼
    Select Tool
        │
        ▼
Generate Structured Action
        │
        ▼
 Validate Against Policy
        │
        ▼
   Execute Over SSH
        │
        ▼
  Observe System State
        │
        └──────────────► Continue Investigation
        │
        ▼
 Determine Root Cause
        │
        ▼
Generate Report + Audit Trail
```

The investigation continues until the agent has gathered enough evidence to produce a diagnosis.

## Features

* Autonomous investigation of Linux infrastructure over SSH
* Iterative LLM-driven reasoning and tool calling
* No human approval required between investigation steps
* Policy-controlled command execution
* YAML-defined SSH command policies
* Structured tool actions instead of arbitrary shell generation
* Transport-agnostic architecture
* Telegram integration
* Live investigation progress updates
* Structured root-cause analysis
* PDF investigation reports
* Complete investigation audit trails
* Persistent machine registry
* Per-machine locking for concurrent investigations
* Containerized daemon deployment
* Host-owned persistent configuration and data

## Secure Command Execution

Allowing an autonomous LLM agent to execute arbitrary shell commands introduces significant security risks.

Raven addresses this with a custom SSH policy engine.

The LLM does not directly generate raw shell commands for execution. Instead, it produces structured tool actions describing the command, arguments, and flags it wants to use.

These actions are validated against YAML-defined command policies before execution.

A policy specifies:

* which commands may be executed
* which flags are allowed
* which arguments are accepted
* which values are prohibited
* how validated command components are rendered into the final executable command

This allows Raven to support commands with different argument and flag layouts without hardcoding command-specific validation logic.

The execution flow is:

```text
LLM Tool Call
      │
      ▼
Structured Command Object
      │
      ▼
YAML Policy Validation
      │
      ▼
Command Template Rendering
      │
      ▼
SSH Execution
```

Commands that do not satisfy the configured policy are rejected before reaching the target machine.

## Investigation Results

Every completed investigation produces two artifacts.

### Investigation Report

A concise report intended for operators and engineers.

It contains:

* target machine information
* reported issue
* investigation summary
* root-cause analysis
* supporting evidence
* recommended actions
* confidence assessment

### Investigation History

A complete audit trail of the autonomous investigation.

The history records every investigation step, including:

* tools invoked
* actions performed
* reasoning behind each action
* observations gathered from the machine
* summarized tool output

This makes Raven's investigations transparent and reviewable instead of exposing only the model's final conclusion.

## Architecture

Raven consists of two primary components.

### Raven CLI

The native CLI manages Raven's local configuration and daemon lifecycle.

```text
raven init
raven vm add
raven vm update
raven vm rm
raven vm list
raven vm show

raven start
raven stop
raven status
raven logs
```

Configuration commands operate directly on Raven's host-owned persistent data.

Daemon lifecycle commands manage the containerized Raven daemon.

### Raven Daemon

The Raven daemon is the long-running investigation service.

It is responsible for:

* running communication transports
* receiving investigation requests
* managing sessions
* executing autonomous investigation agents
* communicating with target machines over SSH
* generating investigation reports
* delivering results to users

The daemon runs inside a Docker container with Raven's persistent data mounted from the host.

```text
Host
│
├── Raven CLI
│
├── ~/.raven/
│   ├── configuration
│   ├── database
│   └── persistent data
│
└── Docker
    │
    └── Raven Daemon
        ├── Transports
        ├── Agent Runtime
        ├── SSH Policy Engine
        ├── LLM Provider
        └── PDF Generation
```

This keeps user data on the host while allowing Raven's runtime dependencies to be distributed as a self-contained container.

## Transport Architecture

Raven's investigation engine is transport-agnostic.

Transports are responsible for accepting user requests and delivering investigation progress and results.

The current implementation integrates with Telegram.

```text
Telegram
    │
    ▼
Telegram Transport
    │
    ▼
Raven Agent Runtime
    │
    ▼
Target Linux Machine
```

Additional transports can be implemented without changing the core investigation engine.

Potential integrations include:

* web applications
* chat platforms
* custom frontends
* APIs

## Network Model

Raven does not require publicly exposed inbound ports.

The daemon initiates outbound connections to:

* communication services such as Telegram
* the configured LLM provider
* registered Linux machines over SSH

```text
Telegram API ◄────── Raven Daemon
LLM Provider ◄────── Raven Daemon
Linux Server ◄────── Raven Daemon
```

## Technology

Raven is built with:

* **Go** — CLI, daemon, agent runtime, transports, SSH execution, and policy engine
* **Gemini** — LLM reasoning and function calling
* **SSH** — controlled remote infrastructure investigation
* **YAML** — declarative command policies
* **SQLite** — persistent machine and application state
* **Telegram Bot API** — current user interface transport
* **Docker** — daemon packaging and deployment
* **PrinceXML** — HTML and CSS to PDF report generation
* **Cobra** — command-line interface
* **Huh** — interactive terminal forms
* **Lip Gloss** — terminal output styling

## Project Status

Raven is currently under active development.

The core investigation pipeline, SSH policy engine, Telegram transport, machine management CLI, structured investigation history, and PDF report generation are implemented or under active integration.

Additional work includes daemon lifecycle management, containerized distribution, improved error handling and graceful shutdown, and further refinement of the investigation workflow.

## License

License information will be added as the project evolves.
