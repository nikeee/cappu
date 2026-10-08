public class Nest {

  static int helper(int arg0) {
    return arg0 * 2;
  }

  static class Counter {
    static int total;
    int n;

    void tick() {
      this.n = this.n + 1;
      total = total + 1;
    }

    int get() {
      return this.n;
    }
  }

  static class Point {
    int x;
    int y;

    Point(int arg0, int arg1) {
      this.x = arg0;
      this.y = arg1;
    }

    int sum() {
      return this.x + this.y;
    }
  }
}
