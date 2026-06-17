package example;

import org.immutables.value.Value;

// The Immutables annotation processor generates ImmutableAnimal (with a builder)
// from this abstract value type during `cappu compile`.
@Value.Immutable
public interface Animal {
  String name();

  int legs();
}
