enum EnumUnqualified {
  A,
  B,
  C;

  private EnumUnqualified() {}

  public static void main(java.lang.String[] arg0) {
    EnumUnqualified var3;
    java.lang.System.out.println(values().length);
    java.lang.System.out.println(valueOf("B").ordinal());
    EnumUnqualified[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.print(var3.name());
      var2++;
    }
    java.lang.System.out.println();
  }

  static {
    /* cappu: dup of a non-trivial value; the bytecode is:
     * 0: new #2
     * 3: dup
     * 4: ldc #69
     * 6: iconst_0
     * 7: invokespecial #70
     * 10: putstatic #72
     * 13: new #2
     * 16: dup
     * 17: ldc #34
     * 19: iconst_1
     * 20: invokespecial #70
     * 23: putstatic #74
     * 26: new #2
     * 29: dup
     * 30: ldc #75
     * 32: iconst_2
     * 33: invokespecial #70
     * 36: putstatic #77
     * 39: iconst_3
     * 40: anewarray #2
     * 43: dup
     * 44: iconst_0
     * 45: getstatic #72
     * 48: aastore
     * 49: dup
     * 50: iconst_1
     * 51: getstatic #74
     * 54: aastore
     * 55: dup
     * 56: iconst_2
     * 57: getstatic #77
     * 60: aastore
     * 61: putstatic #60
     * 64: return
     */
  }
}
