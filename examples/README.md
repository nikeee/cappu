# Examples

Each directory is a self-contained cappu project:

```sh
cd gson-app        # or mapstruct-app
cappu install      # dependencies (and annotation processors) into lib/
cappu compile      # build the fat jar into dist/
java -jar dist/gson-app.jar
```

- **gson-app** - a one-file project using Gson from Maven Central.
- **mapstruct-app** - MapStruct's annotation processor generates the
  mapper implementation during `cappu compile` (#7).

`src/examples.test.ts` builds and runs both end-to-end.
