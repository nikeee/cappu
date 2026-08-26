enum EnumAbstract {
  LOW,
  HIGH;

  private EnumAbstract() {}

  public abstract int rank();

  public static void main(java.lang.String[] arg0) {
    EnumAbstract var3;
    EnumAbstract[] var1 = values();
    int var2 = 0;
    while (var2 < var1.length) {
      var3 = var1[var2];
      java.lang.System.out.println(var3.name() + var3.rank());
      var2++;
    }
  }

  static {
    /* cappu: an inner class constructor; the bytecode is:
     * 0: new #72
     * 3: dup
     * 4: ldc #73
     * 6: iconst_0
     * 7: invokespecial #74
     * 10: putstatic #76
     * 13: new #78
     * 16: dup
     * 17: ldc #79
     * 19: iconst_1
     * 20: invokespecial #80
     * 23: putstatic #82
     * 26: iconst_2
     * 27: anewarray #2
     * 30: dup
     * 31: iconst_0
     * 32: getstatic #76
     * 35: aastore
     * 36: dup
     * 37: iconst_1
     * 38: getstatic #82
     * 41: aastore
     * 42: putstatic #59
     * 45: return
     */
  }
}
