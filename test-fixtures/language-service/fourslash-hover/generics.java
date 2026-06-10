import java.util.List;
import java.util.Map;
import java.util.ArrayList;

class Box</*tparamDecl*/T extends CharSequence> {
  /*tparamUse*/T value;
  T get() { return value; }
  void set(T t) { value = t; }
}

class Generics {
  static <U extends Comparable<U>> U /*maxDecl*/max(U a, U b) {
    return a.compareTo(b) >= 0 ? /*aUse*/a : b;
  }

  void use(List<String> names, Map<String, List<Integer>> registry, Box<String> box) {
    names./*listGet*/get(0);
    names./*listAdd*/add("x");
    registry./*mapGet*/get("k");
    String s = box./*boxField*/value;
    box./*boxGet*/get();
    box./*boxSet*/set("v");
    /*maxUse*/max(1, 2);
    List<Integer> copy = new ArrayList<>();
    copy./*copyAdd*/add(3);
  }
}
