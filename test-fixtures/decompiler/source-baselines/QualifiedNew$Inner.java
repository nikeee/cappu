class QualifiedNew$Inner {
  int v;
  final QualifiedNew this$0;

  QualifiedNew$Inner(QualifiedNew arg0, int arg1) {
    /* cappu: constructor call is not first; the bytecode is:
     * 0: aload_0
     * 1: aload_1
     * 2: putfield #12
     * 5: aload_0
     * 6: invokespecial #15
     * 9: aload_0
     * 10: iload_2
     * 11: putfield #17
     * 14: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  int sum() {
    return this.v + this.this$0.x;
  }
}
