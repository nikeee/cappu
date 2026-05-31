import java.util.List;
import java.util.Map;

class Inference {
  Map<String, List<Integer>> /*mapField*/registry;

  void locals(List<String> xs, String[] arr) {
    var /*s*/s = "hello";
    var /*n*/n = 1 + 2;
    var /*b*/b = xs.isEmpty();
    for (var /*item*/item : xs) {
      use(/*itemUse*/item);
    }
    for (var /*elem*/elem : arr) {
      use(/*elemUse*/elem);
    }
  }
}
