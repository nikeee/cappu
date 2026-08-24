public class ControlFlow {

  static int sum(int arg0) {
    /* cappu: unsupported instruction if_icmpge; the bytecode is:
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
    /* cappu: unsupported instruction if_icmpge; the bytecode is:
     * 0: iload_0
     * 1: iconst_0
     * 2: if_icmpge 8
     * 5: iload_0
     * 6: ineg
     * 7: ireturn
     * 8: iload_0
     * 9: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static int countdown(int arg0) {
    /* cappu: unsupported instruction if_icmple; the bytecode is:
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
    /* cappu: unsupported instruction if_icmplt; the bytecode is:
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
    /* cappu: unsupported instruction if_icmplt; the bytecode is:
     * 0: iload_0
     * 1: iload_1
     * 2: if_icmplt 10
     * 5: iload_0
     * 6: iload_2
     * 7: if_icmple 14
     * 10: iconst_0
     * 11: goto 15
     * 14: iconst_1
     * 15: ireturn
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
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
