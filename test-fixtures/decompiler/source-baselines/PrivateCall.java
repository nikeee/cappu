class PrivateCall {

  private int secret(int arg0) {
    return arg0 * 2;
  }

  int use(int arg0) {
    /* cappu: unsupported instruction invokevirtual; the bytecode is:
     * 0: aload_0
     * 1: iload_1
     * 2: invokevirtual #15
     * 5: iconst_1
     * 6: iadd
     * 7: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
