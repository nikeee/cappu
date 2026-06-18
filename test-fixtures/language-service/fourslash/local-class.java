class Use {
  void m() {
    class Box {
      int value;
      int get() { return value; }
      void set(int v) { value = v; }
    }
    Box b = new Box();
    b./*member*/
  }
}
