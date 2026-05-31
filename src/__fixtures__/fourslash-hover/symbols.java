import java.util.List;

class Container {
  int count;
  String label;

  int total() {
    int local = /*count*/count;
    return /*local*/local + /*total*/total();
  }

  void take(String /*paramDecl*/arg) {
    use(/*paramUse*/arg);
  }
}

interface Shape {}

enum Color { RED, GREEN }

record Point(int x, int y) {}

class Box<T> {
  T value;
  /*Tref*/T get() { return /*valueField*/value; }
}

class Use {
  /*Container*/Container container;
  /*Shape*/Shape shape;
  /*Color*/Color color = Color./*RED*/RED;
  /*Point*/Point point;
  /*List*/List<String> names;
  /*Box*/Box<String> box;
}
