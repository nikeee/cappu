class BoundErasure {
  java.lang.CharSequence v;

  java.lang.CharSequence get() {
    return this.v;
  }

  int len() {
    /* cappu: unsupported instruction invokeinterface; the bytecode is:
     * 0: aload_0
     * 1: getfield #17
     * 4: invokeinterface #26,  1
     * 9: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static int cmp(java.lang.Comparable arg0, java.lang.Comparable arg1) {
    /* cappu: unsupported instruction invokeinterface; the bytecode is:
     * 0: aload_0
     * 1: aload_1
     * 2: invokeinterface #34,  2
     * 7: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static java.lang.Object id(java.lang.Object arg0) {
    return arg0;
  }
}
