class StaticFields {
  static int counter;
  static long total;
  int x;
  long y;

  static int getC() {
    return counter;
  }

  static void setC(int arg0) {
    counter = arg0;
  }

  int getX() {
    return this.x;
  }

  void setX(int arg0) {
    this.x = arg0;
  }

  static long getT() {
    return total;
  }

  void setY(long arg0) {
    this.y = arg0;
  }
}
