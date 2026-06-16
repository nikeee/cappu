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

`src/examples.test.ts` builds and runs all three end-to-end.
