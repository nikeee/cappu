# platform-app

A one-file project over the `cappu.json` dependency features Java developers
know from Maven and Gradle:

- **`platform`** - a BOM. `com.fasterxml.jackson:jackson-bom` pins every Jackson
  module, so the `implementation` entries for `jackson-databind` and
  `jackson-datatype-jsr310` leave their version empty (`""`) and take it from
  the BOM. `cappu tree` marks those roots `(managed)`.
- **exclusions** - the object form `{ "version": "...", "exclude": [...] }`
  keeps Guava's annotation-only transitives (`jspecify`,
  `error_prone_annotations`, `j2objc-annotations`) off the classpath.
- **`compileOnly`** - `org.jspecify:jspecify` is on javac's classpath but not
  in the fat jar (Maven `provided`): annotations are not needed at run time.
- **`runtimeOnly`** - the H2 JDBC driver is in the fat jar but not on javac's
  classpath (Maven `runtime`): `Main.java` only talks to `java.sql`.

```sh
cappu install                   # .cappu/lib/classes, compile-only/ and runtime-only/
cappu compile                   # builds the fat jar into dist/
java -jar dist/platform-app.jar
```

The jar prints a Jackson-serialized record, the first element of a Guava list
and the result of an H2 query:

```
{"name":"1.0.0","date":"2026-01-01"}
guava
2
```

The two scoped jars live in their own directories, and only one of them ends up
in the fat jar:

```sh
ls .cappu/lib/compile-only                              # jspecify-1.0.0.jar
ls .cappu/lib/runtime-only                              # h2-2.5.252.jar
unzip -l dist/platform-app.jar | grep -c org/jspecify   # 0
```

Another Jackson module needs no version on the command line either:
`cappu add implementation com.fasterxml.jackson.module:jackson-module-parameter-names`
writes `""` because the platform manages it.
