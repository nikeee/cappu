# Examples

Each directory is a self-contained cappu project. Dependencies install into
`.cappu/lib/` (cappu-managed, gitignored) and build output goes to `dist/`.

- **gson-app** - a one-file project using Gson from Maven Central. Build the
  fat jar and run it:

  ```sh
  cd gson-app
  cappu install                 # downloads gson into .cappu/lib/classes
  cappu compile                 # builds the fat jar into dist/
  java -jar dist/gson-app.jar
  ```

- **mapstruct-app** - MapStruct's annotation processor (resolved into
  `.cappu/lib/processors`) generates the mapper implementation during
  `cappu compile` (#7). Same install / compile / `java -jar dist/mapstruct-app.jar` flow.

- **junit-app** - `cappu test` compiles `src/test/java` and runs the JUnit
  Platform console launcher over it (#16):

  ```sh
  cd junit-app
  cappu install                 # junit-jupiter into .cappu/lib/test-classes
  cappu test
  ```

- **audit-app** - pinned to a deliberately old, vulnerable Log4j so
  `cappu audit` has advisories to report; it scans the transitive graph
  (OSV.dev) and prints the dependency tree that pulls each one in:

  ```sh
  cd audit-app
  cappu audit                   # exits non-zero, lists the advisories
  ```

- **resources-app** - reads a `src/main/resources` file at runtime (bundled
  into the fat jar by `cappu compile`) and a `src/test/resources` file from a
  test (on the `cappu test` classpath).

- **spring-boot-app** - a minimal Spring Boot app (latest Spring Boot). cappu
  resolves the whole starter dependency tree and compiles it; it runs from a
  classpath of the individual jars (not a fat jar - Spring relies on each jar's
  separate `META-INF` for auto-configuration):

  ```sh
  cd spring-boot-app
  cappu install                 # the spring-boot-starter tree into .cappu/lib/classes
  cappu compile -o classes      # app classes into dist/
  java -cp "dist:.cappu/lib/classes/*" com.example.App
  ```

`src/examples.test.ts` builds, runs, tests and audits every example end-to-end.
