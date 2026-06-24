# Examples

Each directory is a self-contained cappu project with its own `README.md`.
Dependencies install into `.cappu/lib/` (cappu-managed, gitignored) and build
output goes to `dist/`.

- [gson-app](gson-app/README.md) - a fat jar using Gson from Maven Central
- [mapstruct-app](mapstruct-app/README.md) - the MapStruct annotation processor
- [immutables-app](immutables-app/README.md) - the Immutables annotation processor
- [junit-app](junit-app/README.md) - `cappu test` with JUnit
- [debug-app](debug-app/README.md) - debugging over the Debug Adapter Protocol
- [audit-app](audit-app/README.md) - `cappu audit` over a vulnerable dependency
- [nullness-app](nullness-app/README.md) - jspecify nullness checking with flow-aware narrowing
- [resources-app](resources-app/README.md) - bundled main and test resources
- [spring-boot-app](spring-boot-app/README.md) - booting Spring Boot from a single fat jar
- [spring-boot-web-app](spring-boot-web-app/README.md) - a Spring Boot web app (embedded Tomcat) from a single fat jar

`src/examples.test.ts` builds, runs, tests and audits every example end-to-end.
