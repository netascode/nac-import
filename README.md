# Overview

This tool automates importing existing ACI objects into a local terraform state file.

Traditionally `terraform import` would need to be run for each MO, which can be a time consuming and error-prone process. This tool reads imports from the terraform plan output and attempts to speed up imports by running `terraform import` requests in parallel.

**Note** that this is not strictly required, but can provide additional assurance that a large terraform plan will not result in production impact in a brownfield ACI deployment.

# Quick Start

Run this tool from within the Terraform root folder. The CLI options are for exceptional cases and typically won't be required.

```
Usage: nac-import [--verbose] [--no-cleanup] [--install]

Options:
  --verbose, -v          Print debug output to CLI
  --no-cleanup           Keep temporary files for RCA
  --install              Install terraform if not found locally
  --help, -h             display this help and exit
  --version              display version and exit
```

# Caveats

Very large imports may take a while due to APIC request throttling limits. Testing has show approximately 300 object imported in 5-7 minutes. This may vary depending on RTT to the APIC, APIC response time, etc.

Not all object types are supported. Specifically, VPC protection groups and OOB addresses may not import. This is due to the DNs for these objects being unknown prior to creation. These objects can safely be created over existing configuration after reviewing the terraform plan output.

This tool has not been tested with a non-local state file, e.g. Terraform cloud, GitHub action runners, etc. It very likely won't work in these cases; however, it will still create a local state file, which can possibly be meged using `terraform state` commands, or minimally used as additional assurance.
