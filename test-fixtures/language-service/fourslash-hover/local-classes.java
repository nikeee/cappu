class Outer {
  int run(int seed) {
    class Counter {
      int /*field*/count;
      Counter(int /*ctorParam*/start) { count = start; }
      int /*incMethod*/inc() { return ++/*fieldUse*/count; }
    }
    Counter /*localVar*/c = new /*ctorRef*/Counter(seed);
    /*cUse*/c./*incUse*/inc();
    return /*cUse2*/c./*incUse2*/inc();
  }
}
