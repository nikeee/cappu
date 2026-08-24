class Nest$Counter {
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
