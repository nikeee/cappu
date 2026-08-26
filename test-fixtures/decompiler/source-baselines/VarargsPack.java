class VarargsPack {

  static int sum(int... arg0) {
    return arg0.length;
  }

  static java.lang.String join(java.lang.String arg0, java.lang.Object... arg1) {
    return arg0;
  }

  int callPrim() {
    return sum(new int[] {1, 2, 3});
  }

  int callEmpty() {
    return sum(new int[0]);
  }

  java.lang.String callMixed() {
    return join("-", new java.lang.Object[] {"a", "b"});
  }
}
