class VarargsPack {

  static int sum(int... arg0) {
    return arg0.length;
  }

  static java.lang.String join(java.lang.String arg0, java.lang.Object... arg1) {
    return arg0;
  }

  int callPrim() {
    /* cappu: dup of a non-trivial value; the bytecode is:
     * 0: iconst_3
     * 1: newarray int
     * 3: dup
     * 4: iconst_0
     * 5: iconst_1
     * 6: iastore
     * 7: dup
     * 8: iconst_1
     * 9: iconst_2
     * 10: iastore
     * 11: dup
     * 12: iconst_2
     * 13: iconst_3
     * 14: iastore
     * 15: invokestatic #18
     * 18: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  int callEmpty() {
    return sum(new int[0]);
  }

  java.lang.String callMixed() {
    /* cappu: dup of a non-trivial value; the bytecode is:
     * 0: ldc #23
     * 2: iconst_2
     * 3: anewarray #4
     * 6: dup
     * 7: iconst_0
     * 8: ldc #25
     * 10: aastore
     * 11: dup
     * 12: iconst_1
     * 13: ldc #27
     * 15: aastore
     * 16: invokestatic #29
     * 19: areturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
