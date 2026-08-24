class CastInstance {

  java.lang.String down(java.lang.Object arg0) {
    return (java.lang.String) arg0;
  }

  java.lang.CharSequence up(java.lang.String arg0) {
    return arg0;
  }

  boolean isStr(java.lang.Object arg0) {
    return arg0 instanceof java.lang.String;
  }

  int[] arr(java.lang.Object arg0) {
    return (int[]) arg0;
  }
}
