record Circle(double radius) {}

class Calc {
  /**
   * Computes the area of a shape.
   * @param s the shape
   */
  double /*area*/area(Object s) {
    return switch (s) {
      case Circle(double /*r*/r) -> 3.14 * r * r;
      default -> 0.0;
    };
  }

  <T> T /*pick*/pick(T x, T y) throws Exception {
    return x;
  }

  int /*plain*/plain(int a, int b) {
    return a + b;
  }
}
