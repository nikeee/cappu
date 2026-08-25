public class ControlFlow {

  static int sum(int arg0) {
    int var1 = 0;
    int var2 = 0;
    while (var2 < arg0) {
      var1 = var1 + var2;
      var2++;
    }
    return var1;
  }

  static int absish(int arg0) {
    if (arg0 < 0) {
      return -arg0;
    }
    return arg0;
  }

  static int countdown(int arg0) {
    int var1 = 0;
    while (arg0 > 0) {
      var1 = var1 + 1;
      arg0 = arg0 - 1;
    }
    return var1;
  }

  static int firstSet(int arg0) {
    int var1 = 0;
    do {
      var1 = var1 + 1;
    } while (var1 < arg0);
    return var1;
  }

  static boolean inRange(int arg0, int arg1, int arg2) {
    return arg0 >= arg1 && arg0 <= arg2;
  }

  public static void main(java.lang.String[] arg0) {
    /* cappu: unsupported instruction invokestatic; the bytecode is:
     * 0: getstatic #26
     * 3: iconst_5
     * 4: invokestatic #28
     * 7: invokevirtual #34
     * 10: getstatic #26
     * 13: bipush -7
     * 15: invokestatic #36
     * 18: invokevirtual #34
     * 21: getstatic #26
     * 24: iconst_3
     * 25: invokestatic #38
     * 28: invokevirtual #34
     * 31: getstatic #26
     * 34: iconst_4
     * 35: invokestatic #40
     * 38: invokevirtual #34
     * 41: getstatic #26
     * 44: iconst_5
     * 45: iconst_1
     * 46: bipush 10
     * 48: invokestatic #42
     * 51: invokevirtual #45
     * 54: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
