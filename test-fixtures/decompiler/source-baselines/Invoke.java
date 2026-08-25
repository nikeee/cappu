class Invoke {

  static int stat() {
    return 1;
  }

  int inst() {
    return 2;
  }

  int callStat() {
    /* cappu: unsupported instruction invokestatic; the bytecode is:
     * 0: invokestatic #16
     * 3: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  int callInst() {
    /* cappu: unsupported instruction invokevirtual; the bytecode is:
     * 0: aload_0
     * 1: invokevirtual #19
     * 4: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  int strLen(java.lang.String arg0) {
    /* cappu: unsupported instruction invokevirtual; the bytecode is:
     * 0: aload_1
     * 1: invokevirtual #26
     * 4: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
