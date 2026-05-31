class Person {
  String name;
  int age;
  String greet() { return name; }
}
class Use {
  void m(Person p) {
    p./*member*/
  }
}
