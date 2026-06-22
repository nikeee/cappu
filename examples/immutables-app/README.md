# immutables-app

The Immutables annotation processor generates `ImmutableAnimal` (a builder +
value type) from a `@Value.Immutable` interface during `cappu compile`:

```sh
cappu install
cappu compile
java -jar dist/immutables-app.jar
```
