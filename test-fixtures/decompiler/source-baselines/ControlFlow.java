public class ControlFlow {

  static int sum(int arg0) {
    /* cappu: loops are not decompiled yet; the bytecode is:
     * 0: iconst_0
     * 1: istore_1
     * 2: iconst_0
     * 3: istore_2
     * 4: iload_2
     * 5: iload_0
     * 6: if_icmpge 19
     * 9: iload_1
     * 10: iload_2
     * 11: iadd
     * 12: istore_1
     * 13: iinc 2, 1
     * 16: goto 4
     * 19: iload_1
     * 20: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static int absish(int arg0) {
    if (arg0 < 0) {
      return -arg0;
    }
    return arg0;
  }

  static int countdown(int arg0) {
    /* cappu: loops are not decompiled yet; the bytecode is:
     * 0: iconst_0
     * 1: istore_1
     * 2: iload_0
     * 3: iconst_0
     * 4: if_icmple 18
     * 7: iload_1
     * 8: iconst_1
     * 9: iadd
     * 10: istore_1
     * 11: iload_0
     * 12: iconst_1
     * 13: isub
     * 14: istore_0
     * 15: goto 2
     * 18: iload_1
     * 19: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static int firstSet(int arg0) {
    /* cappu: loops are not decompiled yet; the bytecode is:
     * 0: iconst_0
     * 1: istore_1
     * 2: iload_1
     * 3: iconst_1
     * 4: iadd
     * 5: istore_1
     * 6: iload_1
     * 7: iload_0
     * 8: if_icmplt 2
     * 11: iload_1
     * 12: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
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
