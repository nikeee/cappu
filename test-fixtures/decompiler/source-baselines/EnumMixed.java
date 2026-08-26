enum EnumMixed {
  PLUS,
  TIMES,
  IDENT;
  private final java.lang.String sym;

  private EnumMixed(java.lang.String arg2) {
    this.sym = arg2;
  }

  public int apply(int arg0, int arg1) {
    return arg0;
  }

  public java.lang.String sym() {
    return this.sym;
  }

  public static void main(java.lang.String[] arg0) {
    EnumMixed var3;
    EnumMixed[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.println(var3.name() + var3.sym() + var3.apply(6, 7));
      var2++;
    }
  }

  static {
    /* cappu: an inner class constructor; the bytecode is:
     * 0: new #80
     * 3: dup
     * 4: ldc #81
     * 6: iconst_0
     * 7: ldc #83
     * 9: invokespecial #85
     * 12: putstatic #87
     * 15: new #89
     * 18: dup
     * 19: ldc #90
     * 21: iconst_1
     * 22: ldc #92
     * 24: invokespecial #93
     * 27: putstatic #95
     * 30: new #2
     * 33: dup
     * 34: ldc #96
     * 36: iconst_2
     * 37: ldc #98
     * 39: invokespecial #99
     * 42: putstatic #101
     * 45: iconst_3
     * 46: anewarray #2
     * 49: dup
     * 50: iconst_0
     * 51: getstatic #87
     * 54: aastore
     * 55: dup
     * 56: iconst_1
     * 57: getstatic #95
     * 60: aastore
     * 61: dup
     * 62: iconst_2
     * 63: getstatic #101
     * 66: aastore
     * 67: putstatic #67
     * 70: return
     */
  }
}
