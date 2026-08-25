public class QualifiedNew {
  int x;

  public QualifiedNew() {
    this.x = 7;
  }

  static int make(QualifiedNew arg0) {
    /* cappu: unsupported instruction new; the bytecode is:
     * 0: new #17
     * 3: dup
     * 4: aload_0
     * 5: dup
     * 6: invokestatic #23
     * 9: pop
     * 10: iconst_5
     * 11: invokespecial #26
     * 14: getfield #29
     * 17: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
