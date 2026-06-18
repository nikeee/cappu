package example;

public class Main {
  public static void main(String[] args) {
    Animal a = ImmutableAnimal.builder().name("Ant").legs(6).build();
    System.out.println(a.name() + " has " + a.legs() + " legs");
  }
}
