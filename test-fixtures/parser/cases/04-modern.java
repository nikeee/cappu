sealed interface Shape permits Circle, Square {}

record Circle(double radius) implements Shape {}

record Square(double side) implements Shape {}

class Areas {
	double area(Shape s) {
		return switch (s) {
			case Circle(double r) -> 3.14 * r * r;
			case Square sq when sq.side() > 0 -> sq.side() * sq.side();
			default -> 0.0;
		};
	}

	java.util.function.Function<Integer, Integer> twice = x -> x * 2;
	Runnable make = Circle::new;
}
