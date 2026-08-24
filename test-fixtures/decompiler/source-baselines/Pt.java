public class Pt {
  int x;
  int y;

  Pt(int arg0, int arg1) {
    /* cappu: unsupported instruction invokespecial; the bytecode is:
     * 0: aload_0
     * 1: invokespecial #12
     * 4: aload_0
     * 5: iload_1
     * 6: putfield #14
     * 9: aload_0
     * 10: iload_2
     * 11: putfield #16
     * 14: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  int sum() {
    return this.x + this.y;
  }
}
