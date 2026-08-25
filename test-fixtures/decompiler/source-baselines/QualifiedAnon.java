public class QualifiedAnon {
  int x;

  public QualifiedAnon() {
    this.x = 7;
  }

  static int use(QualifiedAnon arg0) {
    /* cappu: an inner class constructor; the bytecode is:
     * 0: new #17
     * 3: dup
     * 4: aload_0
     * 5: dup
     * 6: invokestatic #23
     * 9: pop
     * 10: iconst_5
     * 11: invokespecial #26
     * 14: astore_1
     * 15: aload_1
     * 16: invokevirtual #32
     * 19: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
