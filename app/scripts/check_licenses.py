#!/usr/bin/env python3
"""
开源组件与许可证（licenses.json）资产完整性与覆盖率校验工具。

该工具用于确保：
  1. app/app/src/main/assets/licenses/licenses.json 格式合法、必填字段完整；
  2. 每个组件声明的 licenseId 都在 licenses 字典中有对应的完整官方协议正文；
  3. app/gradle/libs.versions.toml 中声明的所有运行时依赖项都已被 licenses.json 覆盖；
  4. 支持 `--generate` 自动依据预定义元数据生成或更新 licenses.json 资产。

Open-source components and licenses (licenses.json) asset integrity & coverage checker.

Ensures:
  1. The JSON schema and required fields of licenses.json are valid;
  2. Every component's licenseId maps to a full official license text in the licenses map;
  3. All runtime libraries declared in libs.versions.toml are covered in licenses.json;
  4. Supports `--generate` to build or update the asset file.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
APP_ROOT = REPO_ROOT / "app"
TOML_FILE = APP_ROOT / "gradle" / "libs.versions.toml"
LICENSES_JSON = APP_ROOT / "app" / "src" / "main" / "assets" / "licenses" / "licenses.json"

REQUIRED_COMPONENT_FIELDS = {"id", "name", "group", "artifact", "version", "licenseId", "url", "category"}

LICENSE_APACHE_2_0 = """                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or exemplary damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS"""

LICENSE_GPL_3_0 = """                    GNU GENERAL PUBLIC LICENSE
                       Version 3, 29 June 2007

 Copyright (C) 2007 Free Software Foundation, Inc. <https://fsf.org/>
 Everyone is permitted to copy and distribute verbatim copies
 of this license document, but changing it is not allowed.

                            Preamble

  The GNU General Public License is a free, copyleft license for
software and other kinds of works.

  The licenses for most software and other practical works are designed
to take away your freedom to share and change the works.  By contrast,
the GNU General Public License is intended to guarantee your freedom to
share and change all versions of a program--to make sure it remains free
software for all its users.  We, the Free Software Foundation, use the
GNU General Public License for most of our software; it applies also to
any other work released this way by its authors.  You can apply it to
your programs, too.

  When we speak of free software, we are referring to freedom, not
price.  Our General Public Licenses are designed to make sure that you
have the freedom to distribute copies of free software (and charge for
them if you wish), that you receive source code or can get it if you
want it, that you can change the software or use pieces of it in new
free programs, and that you know you can do these things.

  To protect your rights, we need to prevent others from denying you
these rights or asking you to surrender the rights.  Therefore, you have
certain responsibilities if you distribute copies of the software, or if
you modify it: responsibilities to respect the freedom of others.

  For example, if you distribute copies of such a program, whether
gratis or for a fee, you must pass on to the recipients the same
freedoms that you received.  You must make sure that they, too, receive
or can get the source code.  And you must show them these terms so they
know their rights.

  Developers that use the GNU GPL protect your rights with two steps:
(1) assert copyright on the software, and (2) offer you this License
giving you legal permission to copy, distribute and/or modify it.

  For the developers' and authors' protection, the GPL clearly explains
that there is no warranty for this free software.  For both users' and
authors' sake, the GPL requires that modified versions be marked as
changed, so that their problems will not be attributed erroneously to
authors of previous versions.

  Some devices are designed to deny users access to install or run
modified versions of the software inside them, although the manufacturer
can do so.  This is fundamentally incompatible with the aim of
protecting users' freedom to change the software.  The systematic
pattern of such abuse occurs in the area of products for individuals to
use, which is precisely where it is most unacceptable.  Therefore, we
have designed this version of the GPL to prohibit the practice for those
products.  If such problems arise substantially in other domains, we
stand ready to extend this provision to those domains in future versions
of the GPL, as needed to protect the freedom of users.

  Finally, every program is threatened constantly by software patents.
States should not allow patents to restrict development and use of
software on general-purpose computers, but in those that do, we wish to
avoid the special danger that patents applied to a free program could
make it effectively proprietary.  To prevent this, the GPL assures that
patents cannot be used to render the program non-free.

  The precise terms and conditions for copying, distribution and
modification follow.

                       TERMS AND CONDITIONS

  0. Definitions.

  "This License" refers to version 3 of the GNU General Public License.

  "Copyright" also means copyright-like laws that apply to other kinds of
works, such as semiconductor masks.

  "The Program" refers to any copyrightable work licensed under this
License.  Each licensee is addressed as "you".  "Licensees" and
"recipients" may be individuals or organizations.

  To "modify" a work means to copy from or adapt all or part of the work
in a fashion requiring copyright permission, other than the making of an
exact copy.  The resulting work is called a "modified version" of the
earlier work or a work "based on" the earlier work.

  A "covered work" means either the unmodified Program or a work based
on the Program.

  To "propagate" a work means to do anything with it that, without
permission, would make you directly or secondarily liable for
infringement under applicable copyright law, except executing it on a
computer or modifying a private copy.  Propagation includes copying,
distribution (with or without modification), making available to the
public, and in some countries other activities as well.

  To "convey" a work means any kind of propagation that enables other
parties to make or receive copies.  Mere interaction with a user through
a computer network, with no transfer of a copy, is not conveying.

  An interactive user interface displays "Appropriate Legal Notices"
to the extent that it includes a convenient and prominently visible
feature that (1) displays an appropriate copyright notice, and (2)
tells the user that there is no warranty for the work (except to the
extent that warranties are provided), that licensees may convey the
work under this License, and how to view a copy of this License.  If
the interface presents a list of user commands or options, such as a
menu, a prominent item in the list meets this criterion.

  1. Source Code.

  The "source code" for a work means the preferred form of the work
for making modifications to it.  "Object code" means any non-source
form of a work.

  A "Standard Interface" means an interface that either is an official
standard defined by a recognized standards body, or, in the case of
interfaces specified for a particular programming language, one that
is widely used among developers working in that language.

  The "System Libraries" of an executable work include anything, other
than the work as a whole, that (a) is included in the normal form of
packaging a Major Component, but which is not part of that Major
Component, and (b) serves only to enable use of the work with that
Major Component, or to implement a Standard Interface for which an
implementation is available to the public in source code form.  A
"Major Component", in this context, means a major essential component
(kernel, window system, and so on) of the specific operating system
(if any) on which the executable work runs, or a compiler used to
produce the work, or an object code interpreter used to run it.

  The "Corresponding Source" for a work in object code form means all
the source code needed to generate, install, and (for an executable
work) run the object code and to modify the work, including scripts to
control those activities.  However, it does not include the work's
System Libraries, or general-purpose tools or generally available free
programs which are used unmodified in performing those activities but
which are not part of the work.  For example, Corresponding Source
includes interface definition files associated with source files for
the work, and the source code for shared libraries and dynamically
linked subprograms that the work is specifically designed to require,
such as by intimate data communication or control flow between those
subprograms and other parts of the work.

  The Corresponding Source need not include anything that users
can regenerate automatically from other parts of the Corresponding
Source.

  The Corresponding Source for a work in source code form is that
same work.

  2. Basic Permissions.

  All rights granted under this License are granted for the term of
copyright on the Program, and are irrevocable provided the stated
conditions are met.  This License explicitly affirms your unlimited
permission to run the unmodified Program.  The output from running a
covered work is covered by this License only if the output, given its
content, constitutes a covered work.  This License acknowledges your
rights of fair use or other equivalent, as provided by copyright law.

  You may make, run and propagate covered works that you do not
convey, without conditions so long as your license otherwise remains
in force.  You may convey covered works to others for the sole purpose
of having them make modifications exclusively for you, or provide you
with facilities for running those works, provided that you comply with
the terms of this License in conveying all material for which you do
not control copyright.  Those who make or run the covered works for
you must do so exclusively on your behalf, under your direction and
control, on terms that prohibit them from making any copies of your
copyrighted material outside their relationship with you.

  Conveying under any other circumstances is permitted solely under
the conditions stated below.  Sublicensing is not allowed; section 10
makes it unnecessary.

  3. Protecting Users' Legal Rights From Anti-Circumvention Law.

  No covered work shall be deemed part of an effective technological
measure under any applicable law fulfilling obligations under article
11 of the WIPO copyright treaty adopted on 20 December 1996, or
similar laws prohibiting or restricting circumvention of such
measures.

  When you convey a covered work, you waive any legal power to forbid
circumvention of technological measures to the extent such circumvention
is effected by exercising rights under this License with respect to
the covered work, and you disclaim any intention to limit operation or
modification of the work as a means of enforcing, against the work's
users, your or third parties' legal rights to forbid circumvention of
technological measures.

  4. Conveying Verbatim Copies.

  You may convey verbatim copies of the Program's source code as you
receive it, in any medium, provided that you conspicuously and
appropriately publish on each copy an appropriate copyright notice;
keep intact all notices stating that this License and any
non-permissive terms added in accord with section 7 apply to the code;
keep intact all notices of the absence of any warranty; and give all
recipients a copy of this License along with the Program.

  You may charge any price or no price for each copy that you convey,
and you may offer support or warranty protection for a fee.

  5. Conveying Modified Source Versions.

  You may convey a work based on the Program, or the modifications to
produce it from the Program, in the form of source code under the
terms of section 4, provided that you also meet all of these conditions:

    a) The work must carry prominent notices stating that you modified
    it, and giving a relevant date.

    b) The work must carry prominent notices stating that it is
    released under this License and any conditions added under section
    7.  This requirement modifies the requirement in section 4 to
    "keep intact all notices".

    c) You must license the entire work, as a whole, under this
    License to anyone who comes into possession of a copy.  This
    License will therefore apply, along with any applicable section 7
    additional terms, to the whole of the work, and all its parts,
    regardless of how they are packaged.  This License gives no
    permission to license the work in any other way, but it does not
    invalidate such permission if you have separately received it.

    d) If the work has interactive user interfaces, each must display
    Appropriate Legal Notices; however, if the Program has interactive
    interfaces that do not display Appropriate Legal Notices, your
    work need not make them do so.

  A compilation of a covered work with other separate and independent
works, which are not by their nature extensions of the covered work,
and which are not combined with it such as to form a larger program,
in or on a volume of a storage or distribution medium, is called an
"aggregate" if the compilation and its resulting copyright are not
used to limit the access or legal rights of the compilation's users
beyond what the individual works permit.  Inclusion of a covered work
in an aggregate does not cause this License to apply to the other
parts of the aggregate.

  6. Conveying Non-Source Forms.

  You may convey a covered work in object code form under the terms
of sections 4 and 5, provided that you also convey the
machine-readable Corresponding Source under the terms of this License,
in one of these ways:

    a) Convey the object code in, or embodied in, a physical product
    (including a physical distribution medium), accompanied by the
    Corresponding Source fixed on a durable physical medium
    customarily used for software interchange.

    b) Convey the object code in, or embodied in, a physical product
    (including a physical distribution medium), accompanied by a
    written offer, valid for at least three years and valid for as
    long as you offer spare parts or customer support for that product
    model, to give anyone who possesses the object code either (1) a
    copy of the Corresponding Source for all the software in the
    product that is covered by this License, on a durable physical
    medium customarily used for software interchange, for a price no
    more than your reasonable cost of physically performing this
    conveying of source, or (2) access to copy the
    Corresponding Source from a network server at no charge.

    c) Convey individual copies of the object code with a copy of the
    written offer to provide the Corresponding Source.  This
    alternative is allowed only occasionally and noncommercially, and
    only if you received the object code with such an offer, in accord
    with subsection 6b.

    d) Convey the object code by offering access from a designated
    place (gratis or for a charge), and offer equivalent access to the
    Corresponding Source in the same way through the same place at no
    further charge.  You need not require recipients to copy the
    Corresponding Source along with the object code.  If the place to
    copy the object code is a network server, the Corresponding Source
    may be on a different server (operated by you or a third party)
    that supports equivalent copying facilities, provided you maintain
    clear directions next to the object code saying where to find the
    Corresponding Source.  Regardless of what server hosts the
    Corresponding Source, you remain obligated to ensure that it is
    available for as long as needed to satisfy these requirements.

    e) Convey the object code using peer-to-peer transmission, provided
    you inform other peers where the object code and Corresponding
    Source of the work are being offered to the general public at no
    charge under subsection 6d.

  A separable portion of the object code, whose source code is excluded
from the Corresponding Source as a System Library, need not be
included in conveying the object code work.

  A "User Product" is either (1) a "consumer product", which means any
tangible personal property which is normally used for personal, family,
or household purposes, or (2) anything designed or sold for incorporation
into a dwelling.  In determining whether a product is a consumer product,
doubtful cases shall be resolved in favor of coverage.  For a particular
product received by a particular user, "normally used" refers to a
typical or common use of that class of product, regardless of the status
of the particular user or of the way in which the particular user
actually uses, or expects or is expected to use, the product.  A product
is a consumer product regardless of whether the product has substantial
commercial, industrial or non-consumer uses, unless such uses represent
the only significant mode of use of the product.

  "Installation Information" for a User Product means any methods,
procedures, authorization keys, or other information required to install
and execute modified versions of a covered work in that User Product from
a modified version of its Corresponding Source.  The information must
suffice to ensure that the continued functioning of the modified object
code is in no case prevented or interfered with solely because
modification has been made.

  If you convey an object code work under this section in, or with, or
specifically for use in, a User Product, and the conveying occurs as
part of a transaction in which the right of possession and use of the
User Product is transferred to the recipient in perpetuity or for a
fixed term (regardless of how the transaction is characterized), the
Corresponding Source conveyed under this section must be accompanied
by the Installation Information.  But this requirement does not apply
if neither you nor any third party retains the ability to install
modified object code on the User Product (for example, the work has
been installed in ROM).

  The requirement to provide Installation Information does not include a
requirement to continue to provide support service, warranty, or updates
for a work that has been modified or installed by the recipient, or for
the User Product in which it has been modified or installed.  Access to a
network may be denied when the modification itself materially and
adversely affects the operation of the network or violates the rules and
protocols for communication across the network.

  Corresponding Source conveyed, and Installation Information provided,
in accord with this section must be in a format that is publicly
documented (and with an implementation available to the public in
source code form), and must require no special password or key for
unpacking, reading or copying.

  7. Additional Terms.

  "Additional permissions" are terms that supplement the terms of this
License by making exceptions from one or more of its conditions.
Additional permissions that are applicable to the entire Program shall
be treated as though they were included in this License, to the extent
that they are valid under applicable law.  If additional permissions
apply only to part of the Program, that part may be used separately
under those permissions, but the entire Program remains governed by
this License without regard to the additional permissions.

  When you convey a copy of a covered work, you may at your option
remove any additional permissions from that copy, or from any part of
it.  (Additional permissions may be written to require their own
removal in certain cases when you modify the work.)  You may place
additional permissions on material, added by you to a covered work,
for which you have or can give appropriate copyright permission.

  Notwithstanding any other provision of this License, for material you
add to a covered work, you may (if authorized by the copyright holders of
that material) supplement the terms of this License with terms:

    a) Disclaiming warranty or limiting liability differently from the
    terms of sections 15 and 16 of this License; or

    b) Requiring preservation of specified reasonable legal notices or
    author attributions in that material or in the Appropriate Legal
    Notices displayed by works containing it; or

    c) Prohibiting misrepresentation of the origin of that material, or
    requiring that modified versions of such material be marked in
    reasonable ways as different from the original version; or

    d) Limiting the use for publicity purposes of names of licensors or
    authors of the material; or

    e) Declining to grant rights under trademark law for use of some
    trade names, trademarks, or service marks; or

    f) Requiring indemnification of licensors and authors of that
    material by anyone who conveys the material (or modified versions of
    it) with contractual assumptions of liability to the recipient, for
    any liability that these contractual assumptions directly impose on
    those licensors and authors.

  All other non-permissive additional terms are considered "further
restrictions" within the meaning of section 10.  If the Program as you
received it, or any part of it, contains a notice stating that it is
governed by this License along with a term that is a further
restriction, you may remove that term.  If a license document contains
a further restriction but permits relicensing or conveying under this
License, you may add to a covered work material governed by the terms
of that license document, provided that the further restriction does
not survive such relicensing or conveying.

  If you add terms to a covered work in accord with this section, you
must place, in the relevant source files, a statement of the
additional terms that apply to those files, or a notice indicating
where to find the applicable terms.

  Additional terms, permissive or non-permissive, may be stated in the
form of a separately written license, or stated as exceptions;
the above requirements apply either way.

  8. Termination.

  You may not propagate or modify a covered work except as expressly
provided under this License.  Any attempt otherwise to propagate or
modify it is void, and will automatically terminate your rights under
this License (including any patent licenses granted under the third
paragraph of section 11).

  However, if you cease all violation of this License, then your
license from a particular copyright holder is reinstated (a)
provisionally, unless and until the copyright holder explicitly and
finally terminates your license, and (b) permanently, if the copyright
holder fails to notify you of the violation by some reasonable means
prior to 60 days after the cessation.

  Moreover, your license from a particular copyright holder is
reinstated permanently if the copyright holder notifies you of the
violation by some reasonable means, this is the first time you have
received notice of violation of this License (for any work) from that
copyright holder, and you cure the violation prior to 30 days after
your receipt of the notice.

  Termination of your rights under this section does not terminate the
licenses of parties who have received copies or rights from you under
this License.  If your rights have been terminated and not permanently
reinstated, you do not qualify to receive new licenses for the same
material under section 10.

  9. Acceptance Not Required for Having Copies.

  You are not required to accept this License in order to receive or
run a copy of the Program.  Ancillary propagation of a covered work
occurring solely as a consequence of using peer-to-peer transmission
to receive a copy likewise does not require acceptance.  However,
nothing other than this License grants you permission to propagate or
modify any covered work.  These actions infringe copyright if you do
not accept this License.  Therefore, by modifying or propagating a
covered work, you indicate your acceptance of this License to do so.

  10. Automatic Licensing of Downstream Recipients.

  Each time you convey a covered work, the recipient automatically
receives a license from the original licensors, to run, modify and
propagate that work, subject to this License.  You are not responsible
for enforcing compliance by third parties with this License.

  An "entity transaction" is a transaction transferring control of an
organization, or substantially all assets of one, or subdividing an
organization, or merging organizations.  If propagation of a covered
work results from an entity transaction, each party to that
transaction who receives a copy of the work also receives whatever
licenses to the work the party's predecessor in interest had or could
give under the previous paragraph, plus a right to possession of the
Corresponding Source of the work from the predecessor in interest, if
the predecessor has it or can get it with reasonable efforts.

  You may not impose any further restrictions on the exercise of the
rights granted or affirmed under this License.  For example, you may
not impose a license fee, royalty, or other charge for exercise of
rights granted under this License, and you may not initiate litigation
(including a cross-claim or counterclaim in a lawsuit) alleging that
any patent claim is infringed by making, using, selling, offering for
sale, or importing the Program or any portion of it.

  11. Patents.

  A "contributor" is a copyright holder who authorizes use under this
License of the Program or a work on which the Program is based.  The
work thus licensed is called the contributor's "contributor version".

  A contributor's "essential patent claims" are all patent claims
owned or controlled by the contributor, whether already acquired or
hereafter acquired, that would be infringed by some manner, permitted
by this License, of making, using, or selling its contributor version,
but do not include claims that would be infringed only as a
consequence of further modification of the contributor version.  For
purposes of this definition, "control" includes the right to grant
patent sublicenses in a manner consistent with the requirements of
this License.

  Each contributor grants you a non-exclusive, worldwide, royalty-free
patent license under the contributor's essential patent claims, to
make, use, sell, offer for sale, import and otherwise run, modify and
propagate the contents of its contributor version.

  In the following three paragraphs, a "patent license" is any express
agreement or commitment, however denominated, not to enforce a patent
(such as an express permission to practice a patent or covenant not to
sue for patent infringement).  To "grant" such a patent license to a
party means to make such an agreement or commitment not to enforce a
patent against the party.

  If you convey a covered work, knowingly relying on a patent license,
and the Corresponding Source of the work is not available for anyone
to copy, free of charge and under the terms of this License, through a
publicly available network server or other readily accessible means,
then you must either (1) cause the Corresponding Source to be so
available, or (2) arrange to deprive yourself of the benefit of the
patent license for this particular work, or (3) arrange, in a manner
consistent with the requirements of this License, to extend the patent
license to downstream recipients.  "Knowingly relying" means you have
actual knowledge that, but for the patent license, your conveying the
covered work in a country, or your recipient's use of the covered work
in a country, would infringe one or more identifiable patents in that
country that you have reason to believe are valid.

  If, pursuant to or in connection with a single transaction or
arrangement, you convey, or propagate by procuring conveyance of, a
covered work, and grant a patent license to some of the parties
receiving the covered work authorizing them to use, propagate, modify
or convey a specific copy of the covered work, then the patent license
you grant is automatically extended to all recipients of the covered
work and works based on it.

  A patent license is "discriminatory" if it does not include within
the scope of its coverage, prohibits the exercise of, or is
conditioned on the non-exercise of one or more of the rights that are
specifically granted under this License.  You may not convey a covered
work if you are a party to an arrangement with a third party that is
in the business of distributing software, under which you make payment
to the third party based on the extent of your activity of conveying
the work, and under which the third party grants, to any of the
parties who would receive the covered work from you, a discriminatory
patent license (a) in connection with copies of the covered work
conveyed by you (or copies made from those copies), or (b) primarily
for and in connection with specific products or compilations that
contain the covered work, unless you entered into that arrangement,
or that patent license was granted, prior to 28 March 2007.

  Nothing in this License shall be construed as excluding or limiting
any implied license or other defenses to infringement that may
otherwise be available to you under applicable patent law.

  12. No Surrender of Others' Freedom.

  If conditions are imposed on you (whether by court order, agreement or
otherwise) that contradict the conditions of this License, they do not
excuse you from the conditions of this License.  If you cannot convey a
covered work so as to satisfy simultaneously your obligations under this
License and any other pertinent obligations, then as a consequence you may
not convey it at all.  For example, if you agree to terms that obligate you
to collect a royalty for further conveying from those to whom you convey
the Program, the only way you could satisfy both those terms and this
License would be to refrain entirely from conveying the Program.

  13. Use with the GNU Affero General Public License.

  Notwithstanding any other provision of this License, you have
permission to link or combine any covered work with a work licensed
under version 3 of the GNU Affero General Public License into a single
combined work, and to convey the resulting work.  The terms of this
License will continue to apply to the part which is the covered work,
but the special requirements of the GNU Affero General Public License,
section 13, concerning interaction through a network will apply to the
combination as such.

  14. Revised Versions of this License.

  The Free Software Foundation may publish revised and/or new versions of
the GNU General Public License from time to time.  Such new versions will
be similar in spirit to the present version, but may differ in detail to
address new problems or concerns.

  Each version is given a distinguishing version number.  If the
Program specifies that a certain numbered version of the GNU General
Public License "or any later version" applies to it, you have the
option of following the terms and conditions either of that numbered
version or of any later version published by the Free Software
Foundation.  If the Program does not specify a version number of the
GNU General Public License, you may choose any version ever published
by the Free Software Foundation.

  If the Program specifies that a proxy can decide which future
versions of the GNU General Public License can be used, that proxy's
public statement of acceptance of a version permanently authorizes you
to choose that version for the Program.

  Later license versions may give you additional or different
permissions.  However, no additional obligations are imposed on any
author or copyright holder as a result of your choosing to follow a
later version.

  15. Disclaimer of Warranty.

  THERE IS NO WARRANTY FOR THE PROGRAM, TO THE EXTENT PERMITTED BY
APPLICABLE LAW.  EXCEPT WHEN OTHERWISE STATED IN WRITING THE COPYRIGHT
HOLDERS AND/OR OTHER PARTIES PROVIDE THE PROGRAM "AS IS" WITHOUT WARRANTY
OF ANY KIND, EITHER EXPRESSED OR IMPLIED, INCLUDING, BUT NOT LIMITED TO,
THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
PURPOSE.  THE ENTIRE RISK AS TO THE QUALITY AND PERFORMANCE OF THE PROGRAM
IS WITH YOU.  SHOULD THE PROGRAM PROVE DEFECTIVE, YOU ASSUME THE COST OF
ALL NECESSARY SERVICING, REPAIR OR CORRECTION.

  16. Limitation of Liability.

  IN NO EVENT UNLESS REQUIRED BY APPLICABLE LAW OR AGREED TO IN WRITING
WILL ANY COPYRIGHT HOLDER, OR ANY OTHER PARTY WHO MODIFIES AND/OR CONVEYS
THE PROGRAM AS PERMITTED ABOVE, BE LIABLE TO YOU FOR DAMAGES, INCLUDING ANY
GENERAL, SPECIAL, INCIDENTAL OR CONSEQUENTIAL DAMAGES ARISING OUT OF THE
USE OR INABILITY TO USE THE PROGRAM (INCLUDING BUT NOT LIMITED TO LOSS OF
DATA OR DATA BEING RENDERED INACCURATE OR LOSSES SUSTAINED BY YOU OR THIRD
PARTIES OR A FAILURE OF THE PROGRAM TO OPERATE WITH ANY OTHER PROGRAMS),
EVEN IF SUCH HOLDER OR OTHER PARTY HAS BEEN ADVISED OF THE POSSIBILITY OF
SUCH DAMAGES.

  17. Interpretation of Sections 15 and 16.

  If the disclaimer of warranty and limitation of liability provided
above cannot be given local legal effect according to their terms,
reviewing courts shall apply local law that most closely approximates
an absolute waiver of all civil liability in connection with the
Program, unless a warranty or assumption of liability accompanies a
copy of the Program in return for a fee.

                     END OF TERMS AND CONDITIONS"""

LICENSE_GPL_2_0 = """                    GNU GENERAL PUBLIC LICENSE
                       Version 2, June 1991

 Copyright (C) 1989, 1991 Free Software Foundation, Inc.
 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA
 Everyone is permitted to copy and distribute verbatim copies
 of this license document, but changing it is not allowed.

                            Preamble

  The licenses for most software are designed to take away your
freedom to share and change it.  By contrast, the GNU General Public
License is intended to guarantee your freedom to share and change free
software--to make sure the software is free for all its users.  This
General Public License applies to most of the Free Software
Foundation's software and to any other program whose authors commit to
using it.  (Some other Free Software Foundation software is covered by
the GNU Library General Public License instead.)  You can apply it to
your programs, too.

  When we speak of free software, we are referring to freedom, not
price.  Our General Public Licenses are designed to make sure that you
have the freedom to distribute copies of free software (and charge for
this service if you wish), that you receive source code or can get it
if you want it, that you can change the software or use pieces of it
in new free programs; and that you know you can do these things.

  To protect your rights, we need to make restrictions that forbid
anyone to deny you these rights or to ask you to surrender the rights.
These restrictions translate to certain responsibilities for you if you
distribute copies of the software, or if you modify it.

  For example, if you distribute copies of such a program, whether
gratis or for a fee, you must give the recipients all the rights that
you have.  You must make sure that they, too, receive or can get the
source code.  And you must show them these terms so they know their
rights.

  We protect your rights with two steps: (1) copyright the software, and
(2) offer you this license which gives you legal permission to copy,
distribute and/or modify the software.

  Also, for each author's protection and ours, we want to make certain
that everyone understands that there is no warranty for this free
software.  If the software is modified by someone else and passed on, we
want its recipients to know that what they have is not the original, so
that any problems introduced by others will not reflect on the original
authors' reputations.

  Finally, any free program is threatened constantly by software
patents.  We wish to avoid the danger that redistributors of a free
program will individually obtain patent licenses, in effect making the
program proprietary.  To prevent this, we have made it clear that any
patent must be licensed for everyone's free use or not licensed at all.

  The precise terms and conditions for copying, distribution and
modification follow.

                    GNU GENERAL PUBLIC LICENSE
   TERMS AND CONDITIONS FOR COPYING, DISTRIBUTION AND MODIFICATION

  0. This License applies to any program or other work which contains
a notice placed by the copyright holder saying it may be distributed
under the terms of this General Public License.  The "Program", below,
refers to any such program or work, and a "work based on the Program"
means either the Program or any derivative work under copyright law:
that is to say, a work containing the Program or a portion of it,
either verbatim or with modifications and/or translated into another
language.  (Hereinafter, translation is included without limitation in
the term "modification".)  Each licensee is addressed as "you".

Activities other than copying, distribution and modification are not
covered by this License; they are outside its scope.  The act of
running the Program is not restricted, and the output from the Program
is covered only if its contents constitute a work based on the
Program (independent of having been made by running the Program).
Whether that is true depends on what the Program does.

  1. You may copy and distribute verbatim copies of the Program's
source code as you receive it, in any medium, provided that you
conspicuously and appropriately publish on each copy an appropriate
copyright notice and disclaimer of warranty; keep intact all the
notices that refer to this License and to the absence of any warranty;
and give any other recipients of the Program a copy of this License
along with the Program.

You may charge a fee for the physical act of transferring a copy, and
you may at your option offer warranty protection in exchange for a fee.

  2. You may modify your copy or copies of the Program or any portion
of it, thus forming a work based on the Program, and copy and
distribute such modifications or work under the terms of Section 1
above, provided that you also meet all of these conditions:

    a) You must cause the modified files to carry prominent notices
    stating that you changed the files and the date of any change.

    b) You must cause any work that you distribute or publish, that in
    whole or in part contains or is derived from the Program or any
    part thereof, to be licensed as a whole at no charge to all third
    parties under the terms of this License.

    c) If the modified program normally reads commands interactively
    when run, you must cause it, when started running for such
    interactive use in the most ordinary way, to print or display an
    announcement including an appropriate copyright notice and a
    notice that there is no warranty (or else, saying that you provide
    a warranty) and that users may redistribute the program under
    these conditions, and telling the user how to view a copy of this
    License.  (Exception: if the Program itself is interactive but
    does not normally print such an announcement, your work based on
    the Program is not required to print an announcement.)

These requirements apply to the modified work as a whole.  If
identifiable sections of that work are not derived from the Program,
and can be reasonably considered independent and separate works in
themselves, then this License, and its terms, do not apply to those
sections when you distribute them as separate works.  But when you
distribute the same sections as part of a whole which is a work based
on the Program, the distribution of the whole must be on the terms of
this License, whose permissions for other licensees extend to the
entire whole, and thus to each and every part regardless of who wrote it.

Thus, it is not the intent of this section to claim rights or contest
your rights to work written entirely by you; rather, the intent is to
exercise the right to control the distribution of derivative or
collective works based on the Program.

In addition, mere aggregation of another work not based on the Program
with the Program (or with a work based on the Program) on a volume of
a storage or distribution medium does not bring the other work under
the scope of this License.

  3. You may copy and distribute the Program (or a work based on it,
under Section 2) in object code or executable form under the terms of
Sections 1 and 2 above provided that you also do one of the following:

    a) Accompany it with the complete corresponding machine-readable
    source code, which must be distributed under the terms of Sections
    1 and 2 above on a medium customarily used for software interchange; or,

    b) Accompany it with a written offer, valid for at least three
    years, to give any third party, for a charge no more than your
    cost of physically performing source distribution, a complete
    machine-readable copy of the corresponding source code, to be
    distributed under the terms of Sections 1 and 2 above on a medium
    customarily used for software interchange; or,

    c) Accompany it with the information you received as to the offer
    to distribute corresponding source code.  (This alternative is
    allowed only for noncommercial distribution and only if you
    received the program in object code or executable form with such
    an offer, in accord with Subsection b above.)

The source code for a work means the preferred form of the work for
making modifications to it.  For an executable work, complete source
code means all the source code for all modules it contains, plus any
associated interface definition files, plus the scripts used to
control compilation and installation of the executable.  However, as a
special exception, the source code distributed need not include
anything that is normally distributed (in either source or binary
form) with the major components (compiler, kernel, and so on) of the
operating system on which the executable runs, unless that component
itself accompanies the executable.

If distribution of executable or object code is made by offering
access to copy from a designated place, then offering equivalent
access to copy the source code from the same place counts as
distribution of the source code, even though third parties are not
compelled to copy the source along with the object code.

  4. You may not copy, modify, sublicense, or distribute the Program
except as expressly provided under this License.  Any attempt
otherwise to copy, modify, sublicense or distribute the Program is
void, and will automatically terminate your rights under this License.
However, parties who have received copies, or rights, from you under
this License will not have their licenses terminated so long as such
parties remain in full compliance.

  5. You are not required to accept this License, since you have not
signed it.  However, nothing else grants you permission to modify or
distribute the Program or its derivative works.  These actions are
prohibited by law if you do not accept this License.  Therefore, by
modifying or distributing the Program (or any work based on the
Program), you indicate your acceptance of this License to do so, and
all its terms and conditions for copying, distributing or modifying
the Program or works based on it.

  6. Each time you redistribute the Program (or any work based on the
Program), the recipient automatically receives a license from the
original licensor to copy, distribute or modify the Program subject to
these terms and conditions.  You may not impose any further
restrictions on the recipients' exercise of the rights granted herein.
You are not responsible for enforcing compliance by third parties to
this License.

  7. If, as a consequence of a court judgment or allegation of patent
infringement or for any other reason (not limited to patent issues),
conditions are imposed on you (whether by court order, agreement or
otherwise) that contradict the conditions of this License, they do not
excuse you from the conditions of this License.  If you cannot
distribute so as to satisfy simultaneously your obligations under this
License and any other pertinent obligations, then as a consequence you
may not distribute the Program at all.  For example, if a patent
license would not permit royalty-free redistribution of the Program by
all those who receive copies directly or indirectly through you, then
the only way you could satisfy both it and this License would be to
refrain entirely from distribution of the Program.

If any portion of this section is held invalid or unenforceable under
any particular circumstance, the balance of the section is intended to
apply and the section as a whole is intended to apply in other
circumstances.

It is not the purpose of this section to induce you to infringe any
patents or other property right claims or to contest validity of any
such claims; this section has the sole purpose of protecting the
integrity of the free software distribution system, which is
implemented by public license practices.  Many people have made
generous contributions to the wide range of software distributed
through that system in reliance on consistent application of that
system; it is up to the author/donor to decide if he or she is willing
to distribute software through any other system and a licensee cannot
impose that choice.

This section is intended to make thoroughly clear what is believed to
be a consequence of the rest of this License.

  8. If the distribution and/or use of the Program is restricted in
certain countries either by patents or by copyrighted interfaces, the
original copyright holder who places the Program under this License
may add an explicit geographical distribution limitation excluding
those countries, so that distribution is permitted only in or among
countries not thus excluded.  In such case, this License incorporates
the limitation as if written in the body of this License.

  9. The Free Software Foundation may publish revised and/or new versions
of the General Public License from time to time.  Such new versions will
be similar in spirit to the present version, but may differ in detail to
address new problems or concerns.

Each version is given a distinguishing version number.  If the Program
specifies a version number of this License which applies to it and "any
later version", you have the option of following the terms and conditions
either of that version or of any later version published by the Free
Software Foundation.  If the Program does not specify a version number of
this License, you may choose any version ever published by the Free Software
Foundation.

  10. If you wish to incorporate parts of the Program into other free
programs whose distribution conditions are different, write to the author
to ask for permission.  For software which is copyrighted by the Free
Software Foundation, write to the Free Software Foundation; we sometimes
make exceptions for this.  Our decision will be guided by the two goals
of preserving the free status of all derivatives of our free software and
of promoting the sharing and reuse of software generally.

                            NO WARRANTY

  11. BECAUSE THE PROGRAM IS LICENSED FREE OF CHARGE, THERE IS NO WARRANTY
FOR THE PROGRAM, TO THE EXTENT PERMITTED BY APPLICABLE LAW.  EXCEPT WHEN
OTHERWISE STATED IN WRITING THE COPYRIGHT HOLDERS AND/OR OTHER PARTIES
PROVIDE THE PROGRAM "AS IS" WITHOUT WARRANTY OF ANY KIND, EITHER EXPRESSED
OR IMPLIED, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.  THE ENTIRE RISK AS
TO THE QUALITY AND PERFORMANCE OF THE PROGRAM IS WITH YOU.  SHOULD THE
PROGRAM PROVE DEFECTIVE, YOU ASSUME THE COST OF ALL NECESSARY SERVICING,
REPAIR OR CORRECTION.

  12. IN NO EVENT UNLESS REQUIRED BY APPLICABLE LAW OR AGREED TO IN WRITING
WILL ANY COPYRIGHT HOLDER, OR ANY OTHER PARTY WHO MODIFIES AND/OR REDISTRIBUTES
THE PROGRAM AS PERMITTED ABOVE, BE LIABLE TO YOU FOR DAMAGES, INCLUDING ANY
GENERAL, SPECIAL, INCIDENTAL OR CONSEQUENTIAL DAMAGES ARISING OUT OF THE
USE OR INABILITY TO USE THE PROGRAM (INCLUDING BUT NOT LIMITED TO LOSS OF
DATA OR DATA BEING RENDERED INACCURATE OR LOSSES SUSTAINED BY YOU OR THIRD
PARTIES OR A FAILURE OF THE PROGRAM TO OPERATE WITH ANY OTHER PROGRAMS),
EVEN IF SUCH HOLDER OR OTHER PARTY HAS BEEN ADVISED OF THE POSSIBILITY OF
SUCH DAMAGES.

                     END OF TERMS AND CONDITIONS"""

LICENSE_AGPL_3_0 = (REPO_ROOT / "LICENSE").read_text(encoding="utf-8")

LICENSE_LGPL_3_0 = """                   GNU LESSER GENERAL PUBLIC LICENSE
                       Version 3, 29 June 2007

 Copyright (C) 2007 Free Software Foundation, Inc. <https://fsf.org/>
 Everyone is permitted to copy and distribute verbatim copies
 of this license document, but changing it is not allowed.

  This version of the GNU Lesser General Public License incorporates
the terms and conditions of version 3 of the GNU General Public
License, supplemented by the additional permissions listed below.

  0. Additional Definitions.

  As used herein, "this License" refers to version 3 of the GNU Lesser
General Public License, and the "GNU GPL" refers to version 3 of the GNU
General Public License.

  "The Library" refers to a covered work governed by this License,
other than an Application or a Combined Work as defined below.

  An "Application" is any work that makes use of an interface provided
by the Library, but which is not otherwise based on the Library.
Defining a subclass of a class defined by the Library is deemed a mode
of using an interface provided by the Library.

  A "Combined Work" is a work produced by combining or linking an
Application with the Library.  The particular version of the Library
with which the Combined Work was made is also called the "Linked
Version".

  The "Minimal Corresponding Source" for a Combined Work means the
Corresponding Source for the Combined Work, excluding any source code
for portions of the Combined Work that, considered in isolation, are
based on the Application, and not on the Linked Version.

  The "Corresponding Application Code" for a Combined Work means the
object code and/or source code for the Application, including any data
and utility programs needed for reproducing the Combined Work from the
Application, but excluding the System Libraries of the Combined Work.

  1. Exception to Section 3 of the GNU GPL.

  You may convey a covered work under sections 3 and 4 of this License
without being bound by section 3 of the GNU GPL.

  2. Conveying Modified Versions.

  If you modify a copy of the Library, and, in your modifications, a
facility refers to a function or data to be supplied by an Application
that uses the facility (other than as an argument passed when the
facility is invoked), then you may convey a copy of the modified
version:

   a) under this License, provided that you make a good faith effort to
   ensure that, in the event an Application does not supply the
   function or data, the facility still operates, and performs
   whatever part of its purpose remains meaningful, or

   b) under the GNU GPL, with none of the additional permissions of
   this License applicable to that copy.

  3. Object Code Incorporating Material from Library Header Files.

  The object code form of an Application may incorporate material from
a header file that is part of the Library.  You may convey such object
code under terms of your choice, provided that, if the incorporated
material is not limited to numerical parameters, data structure
layouts and accessors, or small macros, inline functions and templates
(ten or fewer lines in length), you do both of the following:

   a) Give prominent notice with each copy of the object code that the
   Library is used in it and that the Library and its use are
   covered by this License.

   b) Accompany the object code with a copy of the GNU GPL and this license
   document.

  4. Combined Works.

  You may convey a Combined Work under terms of your choice that, taken
together, effectively do not restrict modification of the portions of
the Library contained in the Combined Work and reverse engineering for
debugging such modifications, if you also do each of the following:

   a) Give prominent notice with each copy of the Combined Work that the
   Library is used in it and that the Library and its use are
   covered by this License.

   b) Accompany the Combined Work with a copy of the GNU GPL and this license
   document.

   c) For a Combined Work that displays copyright notices during
   execution, include the copyright notice for the Library among
   these notices, as well as a reference directing the user to the
   copies of the GNU GPL and this license document.

   d) Do one of the following:

       0) Convey the Minimal Corresponding Source under the terms of this
       License, and the Corresponding Application Code in a form
       suitable for, and under terms that permit, the user to
       recombine or relink the Application with a modified version of
       the Linked Version to produce a modified Combined Work, in the
       manner specified by section 6 of the GNU GPL for conveying
       Corresponding Source.

       1) Use a suitable shared library mechanism for linking with the
       Library.  A suitable mechanism is one that (a) uses at run time
       a copy of the Library already present on the user's computer
       system, and (b) will operate properly with a modified version
       of the Library that is interface-compatible with the Linked
       Version.

   e) Provide Installation Information, but only if you would otherwise
   be required to provide such information under section 6 of the
   GNU GPL, and only to the extent that such information is
   necessary to install and execute a modified version of the
   Combined Work produced by recombining or relinking the
   Application with a modified version of the Linked Version.

  5. Revised Versions of the GNU Lesser General Public License.

  The Free Software Foundation may publish revised and/or new versions
of the GNU Lesser General Public License from time to time.  Such new
versions will be similar in spirit to the present version, but may
differ in detail to address new problems or concerns.

  Each version is given a distinguishing version number.  If the
Library as you received it specifies that a certain numbered version
of the GNU Lesser General Public License "or any later version"
applies to it, you have the option of following the terms and
conditions either of that published version or of any later version
published by the Free Software Foundation.  If the Library as you
received it does not specify a version number of the GNU Lesser
General Public License, you may choose any version of the GNU Lesser
General Public License ever published by the Free Software Foundation.

  If the Library as you received it specifies that a proxy can decide
whether future versions of the GNU Lesser General Public License shall
apply, that proxy's public statement of acceptance of any version gives
you permanent authorization to choose that version for the Library.

                     END OF TERMS AND CONDITIONS"""

LICENSE_BSD_3_CLAUSE = """Copyright (c) 1997-2024 University of Cambridge
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

    * Redistributions of source code must retain the above copyright notice,
      this list of conditions and the following disclaimer.

    * Redistributions in binary form must reproduce the above copyright
      notice, this list of conditions and the following disclaimer in the
      documentation and/or other materials provided with the distribution.

    * Neither the name of the University of Cambridge nor the names of its
      contributors may be used to endorse or promote products derived from
      this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE."""

LICENSE_MIT = """MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE."""

LICENSE_PUBLIC_DOMAIN = """XZ for Java - Public Domain Notice

XZ for Java is in the public domain.

You can do whatever you want with this software. You can compile it,
modify it, distribute it, sell it, or incorporate it into other software,
without any restrictions.

This software is provided "as is", without any express or implied
warranty. In no event shall the authors be held liable for any damages
arising from the use of this software."""

LICENSE_BOUNCY_CASTLE_MIT = """MIT License

Copyright (c) 2000-2026 The Legion of the Bouncy Castle Inc. (https://www.bouncycastle.org)

Permission is hereby granted, free of charge, to any person obtaining a copy of this software
and associated documentation files (the "Software"), to deal in the Software without restriction,
including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense,
and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial
portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED,
INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR
PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
DEALINGS IN THE SOFTWARE."""

LICENSE_BORINGSSL = """BoringSSL is a fork of OpenSSL. As such, large parts of it fall under OpenSSL
licensing. Files that are completely new have a Google copyright and an ISC
license. This license is reproduced at the bottom of this file.

Contributors to BoringSSL are required to follow the CLA rules for Chromium:
https://cla.developers.google.com/clas

Files in third_party/ have their own licenses, as described therein. The MIT
license, for third_party/fiat, which, unlike other third_party directories, is
compiled into non-test libraries, is included below.

The OpenSSL toolkit stays under a dual license, i.e. both the conditions of the
OpenSSL License and the original SSLeay license apply to the toolkit. See below
for the actual license texts. Actually both licenses are BSD-style Open Source
licenses. In case of any license issues related to OpenSSL please contact
openssl-core@openssl.org.

The following are Google-internal bug numbers where explicit permission from
some authors is recorded for use of their work. (This is purely for our own
record keeping.)
  27287199
  27287880
  27287883
  263291445

  OpenSSL License
  ---------------

/* ====================================================================
 * Copyright (c) 1998-2011 The OpenSSL Project.  All rights reserved.
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions
 * are met:
 *
 * 1. Redistributions of source code must retain the above copyright
 *    notice, this list of conditions and the following disclaimer.
 *
 * 2. Redistributions in binary form must reproduce the above copyright
 *    notice, this list of conditions and the following disclaimer in
 *    the documentation and/or other materials provided with the
 *    distribution.
 *
 * 3. All advertising materials mentioning features or use of this
 *    software must display the following acknowledgment:
 *    "This product includes software developed by the OpenSSL Project
 *    for use in the OpenSSL Toolkit. (http://www.openssl.org/)"
 *
 * 4. The names "OpenSSL Toolkit" and "OpenSSL Project" must not be used to
 *    endorse or promote products derived from this software without
 *    prior written permission. For written permission, please contact
 *    openssl-core@openssl.org.
 *
 * 5. Products derived from this software may not be called "OpenSSL"
 *    nor may "OpenSSL" appear in their names without prior written
 *    permission of the OpenSSL Project.
 *
 * 6. Redistributions of any form whatsoever must retain the following
 *    acknowledgment:
 *    "This product includes software developed by the OpenSSL Project
 *    for use in the OpenSSL Toolkit (http://www.openssl.org/)"
 *
 * THIS SOFTWARE IS PROVIDED BY THE OpenSSL PROJECT ``AS IS'' AND ANY
 * EXPRESSED OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
 * IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
 * PURPOSE ARE DISCLAIMED.  IN NO EVENT SHALL THE OpenSSL PROJECT OR
 * ITS CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
 * SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT
 * NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
 * LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
 * HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT,
 * STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
 * ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED
 * OF THE POSSIBILITY OF SUCH DAMAGE.
 * ====================================================================
 *
 * This product includes cryptographic software written by Eric Young
 * (eay@cryptsoft.com).  This product includes software written by Tim
 * Hudson (tjh@cryptsoft.com).
 *
 */

 Original SSLeay License
 -----------------------

/* Copyright (C) 1995-1998 Eric Young (eay@cryptsoft.com)
 * All rights reserved.
 *
 * This package is an SSL implementation written
 * by Eric Young (eay@cryptsoft.com).
 * The implementation was written so as to conform with Netscapes SSL.
 *
 * This library is free for commercial and non-commercial use as long as
 * the following conditions are aheared to.  The following conditions
 * apply to all code found in this distribution, be it the RC4, RSA,
 * lhash, DES, etc., code; not just the SSL code.  The SSL documentation
 * included with this distribution is covered by the same copyright terms
 * except that the holder is Tim Hudson (tjh@cryptsoft.com).
 *
 * Copyright remains Eric Young's, and as such any Copyright notices in
 * the code are not to be removed.
 * If this package is used in a product, Eric Young should be given attribution
 * as the author of the parts of the library used.
 * This can be in the form of a textual message at program startup or
 * in documentation (online or textual) provided with the package.
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions
 * are met:
 * 1. Redistributions of source code must retain the copyright
 *    notice, this list of conditions and the following disclaimer.
 * 2. Redistributions in binary form must reproduce the above copyright
 *    notice, this list of conditions and the following disclaimer in the
 *    documentation and/or other materials provided with the distribution.
 * 3. All advertising materials mentioning features or use of this software
 *    must display the following acknowledgement:
 *    "This product includes cryptographic software written by
 *     Eric Young (eay@cryptsoft.com)"
 *    The word 'cryptographic' can be left out if the rouines from the library
 *    being used are not cryptographic related :-).
 * 4. If you include any Windows specific code (or a derivative thereof) from
 *    the apps directory (application code) you must include an acknowledgement:
 *    "This product includes software written by Tim Hudson (tjh@cryptsoft.com)"
 *
 * THIS SOFTWARE IS PROVIDED BY ERIC YOUNG ``AS IS'' AND
 * ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
 * IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
 * ARE DISCLAIMED.  IN NO EVENT SHALL THE AUTHOR OR CONTRIBUTORS BE LIABLE
 * FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
 * DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
 * OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
 * HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
 * LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY
 * OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
 * SUCH DAMAGE.
 *
 * The licence and distribution terms for any publically available version or
 * derivative of this code cannot be changed.  i.e. this code cannot simply be
 * copied and put under another distribution licence
 * [including the GNU Public Licence.]
 */


ISC license used for completely new code in BoringSSL:

/* Copyright 2015 The BoringSSL Authors
 *
 * Permission to use, copy, modify, and/or distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY
 * SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION
 * OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN
 * CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE. */


The code in third_party/fiat carries the MIT license:

Copyright (c) 2015-2016 the fiat-crypto authors (see
https://github.com/mit-plv/fiat-crypto/blob/master/AUTHORS).

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


Licenses for support code
-------------------------

Parts of the TLS test suite are under the Go license. This code is not included
in BoringSSL (i.e. libcrypto and libssl) when compiled, however, so
distributing code linked against BoringSSL does not trigger this license:

Copyright (c) 2009 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.


BoringSSL uses the Chromium test infrastructure to run a continuous build,
trybots etc. The scripts which manage this, and the script for generating build
metadata, are under the Chromium license. Distributing code linked against
BoringSSL does not trigger this license.

Copyright 2015 The Chromium Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE."""

LICENSE_USEANIMATIONS_MIT = """MIT License

Copyright (c) 2018-2026 useAnimations (https://useanimations.com)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE."""

LICENSES_CATALOG = {
    'MIT-BouncyCastle': {
        'name': 'Bouncy Castle MIT License',
        'spdxId': 'MIT',
        'url': 'https://github.com/bcgit/bc-java/blob/r1rv86/LICENSE.html',
        "text": LICENSE_BOUNCY_CASTLE_MIT.strip(),
    },
    'BoringSSL': {
        'name': 'BoringSSL License Notices',
        'spdxId': 'ISC AND OpenSSL AND MIT',
        'url': 'https://github.com/google/boringssl/blob/571a7432a19592c620fa316abde47770dad4f82b/LICENSE',
        "text": LICENSE_BORINGSSL.strip(),
    },

    'MIT-UseAnimations': {
        'name': 'useAnimations MIT License',
        'spdxId': 'MIT',
        'url': 'https://github.com/useAnimations/react-useanimations/blob/88b845d9a4330b95bc7f456a17f32ce54157b32f/LICENSE',
        "text": LICENSE_USEANIMATIONS_MIT.strip(),
    },
    "MIT-MaterialKolor": {'name': 'MaterialKolor MIT License', 'spdxId': 'MIT', 'url': 'https://github.com/jordond/MaterialKolor/blob/main/LICENSE', 'text': 'MIT License\n\nCopyright (c) 2025 Jordon de Hoog\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\nof this software and associated documentation files (the "Software"), to deal\nin the Software without restriction, including without limitation the rights\nto use, copy, modify, merge, publish, distribute, sublicense, and/or sell\ncopies of the Software, and to permit persons to whom the Software is\nfurnished to do so, subject to the following conditions:\n\nThe above copyright notice and this permission notice shall be included in all\ncopies or substantial portions of the Software.\n\nTHE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR\nIMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,\nFITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE\nAUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER\nLIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,\nOUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE\nSOFTWARE.'},
    "Apache-2.0": {
        "name": "Apache License 2.0",
        "spdxId": "Apache-2.0",
        "url": "https://www.apache.org/licenses/LICENSE-2.0",
        "text": LICENSE_APACHE_2_0.strip()
    },
    "GPL-3.0": {
        "name": "GNU General Public License v3.0",
        "spdxId": "GPL-3.0-only",
        "url": "https://www.gnu.org/licenses/gpl-3.0.html",
        "text": LICENSE_GPL_3_0.strip()
    },
    "GPL-2.0": {
        "name": "GNU General Public License v2.0",
        "spdxId": "GPL-2.0-only",
        "url": "https://www.gnu.org/licenses/old-licenses/gpl-2.0.html",
        "text": LICENSE_GPL_2_0.strip()
    },
    "AGPL-3.0": {
        "name": "GNU Affero General Public License v3.0",
        "spdxId": "AGPL-3.0-only",
        "url": "https://www.gnu.org/licenses/agpl-3.0.html",
        "text": LICENSE_AGPL_3_0.strip()
    },
    "LGPL-3.0": {
        "name": "GNU Lesser General Public License v3.0",
        "spdxId": "LGPL-3.0-or-later",
        "url": "https://www.gnu.org/licenses/lgpl-3.0.html",
        "text": LICENSE_LGPL_3_0.strip()
    },
    "BSD-3-Clause": {
        "name": "BSD 3-Clause \"New\" or \"Revised\" License",
        "spdxId": "BSD-3-Clause",
        "url": "https://opensource.org/licenses/BSD-3-Clause",
        "text": LICENSE_BSD_3_CLAUSE.strip()
    },
    "MIT": {
        "name": "MIT License",
        "spdxId": "MIT",
        "url": "https://opensource.org/licenses/MIT",
        "text": LICENSE_MIT.strip()
    },
    "Public-Domain": {
        "name": "Public Domain / Dedicated Notice",
        "spdxId": "0BSD",
        "url": "https://tukaani.org/xz/java.html",
        "text": LICENSE_PUBLIC_DOMAIN.strip()
    }
}

COMPONENTS_DATA = [
    {
        'id': 'adb-bcpkix',
        'name': 'Bouncy Castle PKIX',
        'group': 'org.bouncycastle',
        'artifact': 'bcpkix-jdk18on',
        'version': '1.86',
        'licenseId': 'MIT-BouncyCastle',
        'url': 'https://github.com/bcgit/bc-java',
        'description': 'Certificate creation for embedded ADB authentication',
        'category': 'android_lib',
        'isCore': False,
        'tomlAlias': 'adb-bcpkix',
    },
    {
        'id': 'adb-boringssl',
        'name': 'BoringSSL for Android',
        'group': 'io.github.vvb2060.ndk',
        'artifact': 'boringssl',
        'version': '20250114',
        'licenseId': 'BoringSSL',
        'url': 'https://github.com/vvb2060/BoringSSL_Android/tree/20250114',
        'description': 'TLS transport for embedded ADB',
        'category': 'android_lib',
        'isCore': False,
        'tomlAlias': 'adb-boringssl',
    },

    {
        'id': 'lottie-compose',
        'name': 'Lottie Compose',
        'group': 'com.airbnb.android',
        'artifact': 'lottie-compose',
        'version': '6.7.1',
        'licenseId': 'Apache-2.0',
        'url': 'https://github.com/airbnb/lottie-android',
        'description': 'Native Compose playback for bundled animated transfer icons',
        'category': 'android_lib',
        'isCore': False,
        'tomlAlias': 'lottie-compose',
    },
    {
        'id': 'useanimations-icons',
        'name': 'useAnimations Icons',
        'group': 'useAnimations',
        'artifact': 'icons',
        'version': '88b845d9a4330b95bc7f456a17f32ce54157b32f',
        'licenseId': 'MIT-UseAnimations',
        'url': 'https://github.com/useAnimations/react-useanimations',
        'description': 'Animated icons for app navigation, settings, actions, and transfer stages',
        'category': 'android_lib',
        'isCore': False,
    },

{'id': 'hidden-api-bypass', 'name': 'Android Hidden API Bypass', 'group': 'org.lsposed.hiddenapibypass', 'artifact': 'hiddenapibypass', 'version': '6.1', 'licenseId': 'Apache-2.0', 'url': 'https://github.com/LSPosed/AndroidHiddenApiBypass', 'description': 'KernelSU predictive-back preference bridge', 'category': 'android_lib', 'isCore': False, 'tomlAlias': 'hidden-api-bypass'},
{'id': 'kernelsu-ui', 'name': 'KernelSU Manager UI', 'group': 'me.weishu.kernelsu', 'artifact': 'manager-ui', 'version': '6e38604', 'licenseId': 'GPL-3.0', 'url': 'https://github.com/tiann/KernelSU', 'description': 'Material and Miuix manager UI source transplants; business adapters are documented', 'category': 'core', 'isCore': True},
{'id': 'miuix-runtime', 'name': 'Miuix UI / Preference / Blur / Shader', 'group': 'top.yukonga.miuix.kmp', 'artifact': 'miuix-runtime', 'version': '0.9.4 (39c40f9)', 'licenseId': 'Apache-2.0', 'url': 'https://github.com/compose-miuix-ui/miuix', 'description': 'Pinned upstream UI sources compiled for JVM 17', 'category': 'core', 'isCore': True},
{'id': 'material-kolor', 'name': 'MaterialKolor', 'group': 'com.materialkolor', 'artifact': 'material-kolor', 'version': '5.0.1', 'licenseId': 'MIT-MaterialKolor', 'url': 'https://github.com/jordond/MaterialKolor', 'description': 'KSU theme and responsive UI dependency', 'category': 'android_lib', 'isCore': False, 'tomlAlias': 'material-kolor'},
{'id': 'material-color-utilities', 'name': 'Material Color Utilities', 'group': 'com.materialkolor', 'artifact': 'material-color-utilities', 'version': '5.0.1', 'licenseId': 'MIT-MaterialKolor', 'url': 'https://github.com/jordond/MaterialKolor', 'description': 'KSU theme and responsive UI dependency', 'category': 'android_lib', 'isCore': False, 'tomlAlias': 'material-color-utilities'},
{'id': 'androidx-material3-window-size-class', 'name': 'AndroidX Material 3 Window Size Class', 'group': 'androidx.compose.material3', 'artifact': 'material3-window-size-class', 'version': '1.5.0-alpha28', 'licenseId': 'Apache-2.0', 'url': 'https://developer.android.com/jetpack/androidx/releases/compose-material3', 'description': 'KSU theme and responsive UI dependency', 'category': 'android_lib', 'isCore': False, 'tomlAlias': 'androidx-material3-window-size'},
    {'id': 'litert',
     'name': 'Google LiteRT',
     'group': 'com.google.ai.edge.litert',
     'artifact': 'litert',
     'version': '2.1.0rc1',
     'licenseId': 'Apache-2.0',
     'url': 'https://github.com/google-ai-edge/LiteRT',
     'description': 'On-device inference runtime used by OCR',
     'category': 'android_lib',
     'isCore': False,
     'tomlAlias': 'litert'},
    {'id': 'miuix-nav',
     'name': 'Miuix Navigation',
     'group': 'top.yukonga.miuix.kmp',
     'artifact': 'miuix-nav',
     'version': '0.9.4 (39c40f9)',
     'licenseId': 'Apache-2.0',
     'url': 'https://github.com/compose-miuix-ui/miuix',
     'description': 'Navigation runtime built from pinned upstream sources for JVM 17',
     'category': 'core',
     'isCore': True},
    {'id': 'miuix-squircle',
     'name': 'Miuix Squircle',
     'group': 'top.yukonga.miuix.kmp',
     'artifact': 'miuix-squircle-android',
     'version': '0.9.4',
     'licenseId': 'Apache-2.0',
     'url': 'https://github.com/compose-miuix-ui/miuix',
     'description': 'Smooth corner clipping for navigation transitions',
     'category': 'android_lib',
     'isCore': False,
     'tomlAlias': 'miuix-squircle'},
    {'id': 'androidx-navigationevent-compose',
     'name': 'AndroidX Navigation Event Compose',
     'group': 'androidx.navigationevent',
     'artifact': 'navigationevent-compose',
     'version': '1.1.2',
     'licenseId': 'Apache-2.0',
     'url': 'https://developer.android.com/jetpack/androidx/releases/navigationevent',
     'description': 'System back progress and navigation event dispatch for Compose',
     'category': 'android_lib',
     'isCore': False,
     'tomlAlias': 'androidx-navigationevent-compose'},
    {'id': 'androidx-foundation',
     'name': 'Jetpack Compose Foundation',
     'group': 'androidx.compose.foundation',
     'artifact': 'foundation',
     'version': 'Compose BOM 2026.05.01',
     'licenseId': 'Apache-2.0',
     'url': 'https://developer.android.com/jetpack/androidx/releases/compose-foundation',
     'description': 'Compose gesture, scrolling, and layout primitives',
     'category': 'android_lib',
     'isCore': False,
     'tomlAlias': 'androidx-foundation'},
    {'id': 'kotlinx-collections-immutable',
     'name': 'Kotlinx Immutable Collections',
     'group': 'org.jetbrains.kotlinx',
     'artifact': 'kotlinx-collections-immutable',
     'version': '0.5.1',
     'licenseId': 'Apache-2.0',
     'url': 'https://github.com/Kotlin/kotlinx.collections.immutable',
     'description': 'Immutable collections used by the navigation runtime',
     'category': 'android_lib',
     'isCore': False,
     'tomlAlias': 'kotlinx-collections-immutable'},

    # 核心与内嵌组件 (Core & Bundled)
    {
        "id": "azurpilot",
        "name": "AzurPilot",
        "group": "com.azurpilot",
        "artifact": "azurpilot",
        "version": "main",
        "licenseId": "GPL-3.0",
        "url": "https://github.com/wess09/AzurPilot",
        "description": "Azur Lane automation tool (upstream core runtime)",
        "category": "core",
        "isCore": True
    },
    {
        "id": "proot",
        "name": "PRoot",
        "group": "net.proot",
        "artifact": "proot",
        "version": "5.1.107",
        "licenseId": "GPL-2.0",
        "url": "https://github.com/proot-me/proot",
        "description": "User-space chroot/mount/bind execution environment",
        "category": "core",
        "isCore": True
    },
    {
        "id": "busybox",
        "name": "BusyBox",
        "group": "net.busybox",
        "artifact": "busybox",
        "version": "1.38.0",
        "licenseId": "GPL-2.0",
        "url": "https://busybox.net/",
        "description": "The Swiss Army Knife of Embedded Linux",
        "category": "core",
        "isCore": True
    },
    {
        "id": "libtalloc",
        "name": "talloc",
        "group": "org.samba.talloc",
        "artifact": "libtalloc",
        "version": "2.4.3",
        "licenseId": "LGPL-3.0",
        "url": "https://talloc.samba.org/",
        "description": "Hierarchical reference-counted memory pool system",
        "category": "core",
        "isCore": True
    },
    {
        "id": "pcre2",
        "name": "PCRE2",
        "group": "org.pcre",
        "artifact": "pcre2",
        "version": "10.49",
        "licenseId": "BSD-3-Clause",
        "url": "https://www.pcre.org/",
        "description": "Perl Compatible Regular Expressions",
        "category": "core",
        "isCore": True
    },
    {
        "id": "libandroid-shmem",
        "name": "libandroid-shmem",
        "group": "com.termux",
        "artifact": "libandroid-shmem",
        "version": "0.7",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/termux/libandroid-shmem",
        "description": "System V shared memory emulation for Android",
        "category": "core",
        "isCore": True
    },
    {
        "id": "libandroid-selinux",
        "name": "libandroid-selinux",
        "group": "com.termux",
        "artifact": "libandroid-selinux",
        "version": "14.0.0",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/termux/libandroid-selinux",
        "description": "SELinux compatibility layer for Android",
        "category": "core",
        "isCore": True
    },
    {
        "id": "scrcpy-command",
        "name": "scrcpy Command Utilities",
        "group": "com.genymobile.scrcpy",
        "artifact": "command",
        "version": "3.1",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Genymobile/scrcpy",
        "description": "Synchronous command execution and process supervision utilities",
        "category": "core",
        "isCore": True
    },
    {
        "id": "alas-aos",
        "name": "ALAS-AOS",
        "group": "com.shinarin",
        "artifact": "alas-aos",
        "version": "main",
        "licenseId": "AGPL-3.0",
        "url": "https://github.com/Shinarin/ALAS-AOS",
        "description": "Android host architecture base for AzurPilot",
        "category": "core",
        "isCore": True
    },

    # Android 运行时依赖项 (Android Runtime Libraries)
    {
        "id": "androidx-core-ktx",
        "name": "AndroidX Core KTX",
        "group": "androidx.core",
        "artifact": "core-ktx",
        "version": "1.19.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/core",
        "description": "Kotlin extensions for Android core framework APIs",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-core-ktx"
    },
    {
        "id": "androidx-core-splashscreen",
        "name": "AndroidX Core SplashScreen",
        "group": "androidx.core",
        "artifact": "core-splashscreen",
        "version": "1.2.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/develop/ui/views/launch/splash-screen",
        "description": "Backwards-compatible splash screen support for Android 12+",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-core-splashscreen"
    },
    {
        "id": "androidx-lifecycle-runtime-ktx",
        "name": "AndroidX Lifecycle Runtime KTX",
        "group": "androidx.lifecycle",
        "artifact": "lifecycle-runtime-ktx",
        "version": "2.11.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/lifecycle",
        "description": "Coroutine extensions for Android lifecycle",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-lifecycle-runtime-ktx"
    },
    {
        "id": "androidx-lifecycle-runtime-compose",
        "name": "AndroidX Lifecycle Runtime Compose",
        "group": "androidx.lifecycle",
        "artifact": "lifecycle-runtime-compose",
        "version": "2.11.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/lifecycle",
        "description": "Compose integrations for Android lifecycle states",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-lifecycle-runtime-compose"
    },
    {
        "id": "androidx-lifecycle-viewmodel-compose",
        "name": "AndroidX Lifecycle ViewModel Compose",
        "group": "androidx.lifecycle",
        "artifact": "lifecycle-viewmodel-compose",
        "version": "2.11.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/lifecycle",
        "description": "ViewModel integration for Jetpack Compose",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-lifecycle-viewmodel-compose"
    },
    {
        "id": "androidx-lifecycle-process",
        "name": "AndroidX Lifecycle Process",
        "group": "androidx.lifecycle",
        "artifact": "lifecycle-process",
        "version": "2.11.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/lifecycle",
        "description": "Process-wide lifecycle monitoring for Android",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-lifecycle-process"
    },
    {
        "id": "androidx-activity-compose",
        "name": "AndroidX Activity Compose",
        "group": "androidx.activity",
        "artifact": "activity-compose",
        "version": "1.13.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/activity",
        "description": "Jetpack Compose integration for Activity APIs",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-activity-compose"
    },
    {
        "id": "androidx-appcompat",
        "name": "AndroidX AppCompat",
        "group": "androidx.appcompat",
        "artifact": "appcompat",
        "version": "1.7.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/appcompat",
        "description": "Android compatibility library for older platform versions",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-appcompat"
    },
    {
        "id": "androidx-biometric",
        "name": "AndroidX Biometric",
        "group": "androidx.biometric",
        "artifact": "biometric",
        "version": "1.2.0-alpha05",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/biometric",
        "description": "Biometric prompt and authentication management",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-biometric"
    },
    {
        "id": "androidx-compose-bom",
        "name": "Jetpack Compose BOM",
        "group": "androidx.compose",
        "artifact": "compose-bom",
        "version": "2026.05.01",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose/bom",
        "description": "Bill of Materials for Jetpack Compose libraries",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-compose-bom"
    },
    {
        "id": "androidx-ui",
        "name": "Jetpack Compose UI",
        "group": "androidx.compose.ui",
        "artifact": "ui",
        "version": "1.8.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose",
        "description": "Core layout, text and rendering primitives for Compose",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-ui"
    },
    {
        "id": "androidx-ui-graphics",
        "name": "Jetpack Compose UI Graphics",
        "group": "androidx.compose.ui",
        "artifact": "ui-graphics",
        "version": "1.8.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose",
        "description": "Graphics and drawing utilities for Compose UI",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-ui-graphics"
    },
    {
        "id": "androidx-ui-tooling-preview",
        "name": "Jetpack Compose UI Tooling Preview",
        "group": "androidx.compose.ui",
        "artifact": "ui-tooling-preview",
        "version": "1.8.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose",
        "description": "Compose tooling support for live layout previews",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-ui-tooling-preview"
    },
    {
        "id": "androidx-material3",
        "name": "Jetpack Compose Material 3",
        "group": "androidx.compose.material3",
        "artifact": "material3",
        "version": "1.5.0-alpha28",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/compose-material3",
        "description": "Material Design 3 components for Jetpack Compose",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-material3"
    },
    {
        "id": "androidx-material-icons-core",
        "name": "Material Icons Core",
        "group": "androidx.compose.material",
        "artifact": "material-icons-core",
        "version": "1.7.8",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose",
        "description": "Core Material Design icon set for Compose",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-material-icons-core"
    },
    {
        "id": "androidx-material-icons-extended",
        "name": "Material Icons Extended",
        "group": "androidx.compose.material",
        "artifact": "material-icons-extended",
        "version": "1.7.8",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/compose",
        "description": "Comprehensive extended Material Design icon set",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-material-icons-extended"
    },
    {
        "id": "androidx-glance",
        "name": "AndroidX Glance",
        "group": "androidx.glance",
        "artifact": "glance",
        "version": "1.1.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/glance",
        "description": "Declarative widgets based on Jetpack Compose runtime",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-glance"
    },
    {
        "id": "androidx-glance-appwidget",
        "name": "AndroidX Glance AppWidget",
        "group": "androidx.glance",
        "artifact": "glance-appwidget",
        "version": "1.1.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/glance",
        "description": "App widget support and RemoteViews binding for Glance",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-glance-appwidget"
    },
    {
        "id": "androidx-glance-material3",
        "name": "AndroidX Glance Material 3",
        "group": "androidx.glance",
        "artifact": "glance-material3",
        "version": "1.1.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/glance",
        "description": "Material 3 theme and components for Glance app widgets",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-glance-material3"
    },
    {
        "id": "androidx-datastore",
        "name": "AndroidX DataStore",
        "group": "androidx.datastore",
        "artifact": "datastore",
        "version": "1.2.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/topic/libraries/architecture/datastore",
        "description": "Asynchronous transactional key-value data storage",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-datastore"
    },
    {
        "id": "androidx-datastore-preferences",
        "name": "AndroidX DataStore Preferences",
        "group": "androidx.datastore",
        "artifact": "datastore-preferences",
        "version": "1.2.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/topic/libraries/architecture/datastore",
        "description": "Preferences DataStore for typed and reactive settings",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-datastore-preferences"
    },
    {
        "id": "androidx-window",
        "name": "AndroidX Window",
        "group": "androidx.window",
        "artifact": "window",
        "version": "1.5.1",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/window",
        "description": "Window metrics and multi-window posture detection",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-window"
    },
    {
        "id": "androidx-browser",
        "name": "AndroidX Browser",
        "group": "androidx.browser",
        "artifact": "browser",
        "version": "1.10.0",
        "licenseId": "Apache-2.0",
        "url": "https://developer.android.com/jetpack/androidx/releases/browser",
        "description": "Custom Tabs and browser intent integration",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "androidx-browser"
    },
    {
        "id": "okhttp",
        "name": "OkHttp",
        "group": "com.squareup.okhttp3",
        "artifact": "okhttp",
        "version": "5.2.1",
        "licenseId": "Apache-2.0",
        "url": "https://square.github.io/okhttp/",
        "description": "Square HTTP and WebSocket client for Android and Java",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "okhttp"
    },
    {
        "id": "koin-android",
        "name": "Koin Android",
        "group": "io.insert-koin",
        "artifact": "koin-android",
        "version": "4.2.1",
        "licenseId": "Apache-2.0",
        "url": "https://insert-koin.io/",
        "description": "Pragmatic lightweight dependency injection for Android",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "koin-android"
    },
    {
        "id": "koin-androidx-compose",
        "name": "Koin Compose",
        "group": "io.insert-koin",
        "artifact": "koin-androidx-compose",
        "version": "4.2.1",
        "licenseId": "Apache-2.0",
        "url": "https://insert-koin.io/",
        "description": "Jetpack Compose injection extensions for Koin",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "koin-androidx-compose"
    },
    {
        "id": "timber",
        "name": "Timber",
        "group": "com.jakewharton.timber",
        "artifact": "timber",
        "version": "5.0.1",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/JakeWharton/timber",
        "description": "Small, extensible logging utility on top of Android Log",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "timber"
    },
    {
        "id": "kotlinx-serialization-json",
        "name": "Kotlinx Serialization JSON",
        "group": "org.jetbrains.kotlinx",
        "artifact": "kotlinx-serialization-json",
        "version": "1.11.0",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Kotlin/kotlinx.serialization",
        "description": "Cross-platform JSON serialization library for Kotlin",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "kotlinx-serialization-json"
    },
    {
        "id": "shizuku-api",
        "name": "Shizuku API",
        "group": "dev.rikka.shizuku",
        "artifact": "api",
        "version": "13.1.5",
        "licenseId": "Apache-2.0",
        "url": "https://shizuku.rikka.app/",
        "description": "Client library to use system APIs directly with ADB privileges",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "shizuku-api"
    },
    {
        "id": "shizuku-provider",
        "name": "Shizuku Provider",
        "group": "dev.rikka.shizuku",
        "artifact": "provider",
        "version": "13.1.5",
        "licenseId": "Apache-2.0",
        "url": "https://shizuku.rikka.app/",
        "description": "Content provider bindings for Shizuku binder exchange",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "shizuku-provider"
    },
    {
        "id": "libsu",
        "name": "libsu",
        "group": "com.github.topjohnwu.libsu",
        "artifact": "core",
        "version": "6.0.0",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/topjohnwu/libsu",
        "description": "Complete Android root shell and root services solution",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "libsu"
    },
    {
        "id": "xx-permissions",
        "name": "XXPermissions",
        "group": "com.github.getActivity",
        "artifact": "XXPermissions",
        "version": "28.0",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/getActivity/XXPermissions",
        "description": "Android runtime and special system permissions requester framework",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "xx-permissions"
    },
    {
        "id": "floatingx",
        "name": "FloatingX",
        "group": "io.github.petterpx",
        "artifact": "floatingx",
        "version": "2.3.7",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Petterpx/FloatingX",
        "description": "Android floating window and overlay control framework",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "floatingx"
    },
    {
        "id": "floatingx-compose",
        "name": "FloatingX Compose",
        "group": "io.github.petterpx",
        "artifact": "floatingx-compose",
        "version": "2.3.7",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Petterpx/FloatingX",
        "description": "Jetpack Compose view support for FloatingX overlays",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "floatingx-compose"
    },
    {
        "id": "commons-compress",
        "name": "Apache Commons Compress",
        "group": "org.apache.commons",
        "artifact": "commons-compress",
        "version": "1.27.1",
        "licenseId": "Apache-2.0",
        "url": "https://commons.apache.org/proper/commons-compress/",
        "description": "Java API for working with tar, zip, ar, cpio and other archive formats",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "commons-compress"
    },
    {
        "id": "tukaani-xz",
        "name": "XZ for Java",
        "group": "org.tukaani",
        "artifact": "xz",
        "version": "1.10",
        "licenseId": "Public-Domain",
        "url": "https://tukaani.org/xz/java.html",
        "description": "Pure Java implementation of XZ data compression",
        "category": "android_lib",
        "isCore": False,
        "tomlAlias": "tukaani-xz"
    },

    # 关键传递依赖 (Key Transitive Dependencies)
    {
        "id": "kotlin-stdlib",
        "name": "Kotlin Standard Library",
        "group": "org.jetbrains.kotlin",
        "artifact": "kotlin-stdlib",
        "version": "2.3.21",
        "licenseId": "Apache-2.0",
        "url": "https://kotlinlang.org/",
        "description": "The Kotlin Standard Library for JVM and Android",
        "category": "android_lib",
        "isCore": False
    },
    {
        "id": "kotlinx-coroutines-core",
        "name": "Kotlin Coroutines Core",
        "group": "org.jetbrains.kotlinx",
        "artifact": "kotlinx-coroutines-core",
        "version": "1.10.2",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Kotlin/kotlinx.coroutines",
        "description": "Core primitives and async/await support for Kotlin",
        "category": "android_lib",
        "isCore": False
    },
    {
        "id": "kotlinx-coroutines-android",
        "name": "Kotlin Coroutines Android",
        "group": "org.jetbrains.kotlinx",
        "artifact": "kotlinx-coroutines-android",
        "version": "1.10.2",
        "licenseId": "Apache-2.0",
        "url": "https://github.com/Kotlin/kotlinx.coroutines",
        "description": "Android Main dispatcher and lifecycle integrations for coroutines",
        "category": "android_lib",
        "isCore": False
    },
    {
        "id": "okio",
        "name": "Okio",
        "group": "com.squareup.okio",
        "artifact": "okio",
        "version": "3.10.2",
        "licenseId": "Apache-2.0",
        "url": "https://square.github.io/okio/",
        "description": "Modern I/O library for Android, Java, and Kotlin",
        "category": "android_lib",
        "isCore": False
    }
]

def parse_toml_libraries(toml_path: Path) -> dict[str, dict[str, str]]:
    """从 libs.versions.toml 提取 libraries 表里的项"""
    text = toml_path.read_text(encoding="utf-8")
    in_libraries = False
    libs: dict[str, dict[str, str]] = {}

    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("[") and line.endswith("]"):
            in_libraries = (line == "[libraries]")
            continue
        if not in_libraries:
            continue

        match = re.match(r"^([\w-]+)\s*=\s*\{(.*)\}", line)
        if match:
            alias = match.group(1)
            raw_props = match.group(2)
            props: dict[str, str] = {}
            for item in re.finditer(r'([\w.]+)\s*=\s*"([^"]+)"', raw_props):
                props[item.group(1)] = item.group(2)
            libs[alias] = props
    return libs

def check_licenses_json(json_path: Path, toml_libs: dict[str, dict[str, str]]) -> list[str]:
    """校验 licenses.json 的完整性与与 toml 依赖的覆盖率"""
    errors: list[str] = []
    if not json_path.is_file():
        return [f"许可证资产文件不存在: {json_path}"]

    try:
        data = json.loads(json_path.read_text(encoding="utf-8"))
    except Exception as e:
        return [f"licenses.json JSON 解析失败: {e}"]

    licenses = data.get("licenses", {})
    components = data.get("components", [])

    # 防止删节版协议通过长度检查；生成资产与校验都复用仓库原文。
    if licenses.get("AGPL-3.0", {}).get("text", "").strip() != LICENSE_AGPL_3_0.strip():
        errors.append("AGPL-3.0 正文与仓库 LICENSE 完整原文不一致")

    if not licenses:
        errors.append("licenses 字典为空，缺少协议正文库")
    if not components:
        errors.append("components 列表为空，缺少开源组件清单")

    for lic_id, lic_info in licenses.items():
        if not isinstance(lic_info, dict):
            errors.append(f"许可证 '{lic_id}' 格式错误，应为对象")
            continue
        if not lic_info.get("name"):
            errors.append(f"许可证 '{lic_id}' 缺少 name 字段")
        if not lic_info.get("text") or len(lic_info.get("text", "").strip()) < 50:
            errors.append(f"许可证 '{lic_id}' 协议正文为空或过短")

    comp_ids = set()
    covered_toml_aliases = set()

    IGNORED_TOML_ALIASES = {
        "junit", "mockk", "kotlinx-coroutines-test", "androidx-junit", "androidx-espresso-core",
        "androidx-ui-test-junit4", "androidx-ui-test-manifest", "androidx-ui-tooling",
        "androidx-test-runner",
        "android-gradlePlugin", "kotlin-gradlePlugin", "snakeyaml-engine",
        "kotlinpoet", "kotlinpoet-ksp", "symbol-processing-api"
    }

    for comp in components:
        cid = comp.get("id")
        if not cid:
            errors.append("存在未设置 id 的组件")
            continue
        if cid in comp_ids:
            errors.append(f"存在重复的组件 ID: {cid}")
        comp_ids.add(cid)

        missing_fields = REQUIRED_COMPONENT_FIELDS - set(comp.keys())
        if missing_fields:
            errors.append(f"组件 '{cid}' 缺少必填字段: {sorted(missing_fields)}")

        lic_id = comp.get("licenseId")
        if lic_id not in licenses:
            errors.append(f"组件 '{cid}' 声明的许可证 '{lic_id}' 在 licenses 字典中未定义")

        toml_alias = comp.get("tomlAlias")
        if toml_alias:
            covered_toml_aliases.add(toml_alias)

    for alias in toml_libs:
        if alias in IGNORED_TOML_ALIASES:
            continue
        if alias not in covered_toml_aliases and alias not in comp_ids:
            errors.append(f"libs.versions.toml 中的运行时依赖 '{alias}' 未在 licenses.json 中收录")

    return errors

def generate_licenses_asset(json_path: Path) -> None:
    """生成 licenses.json 资产文件"""
    json_path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "version": 1,
        "licenses": LICENSES_CATALOG,
        "components": COMPONENTS_DATA
    }
    json_path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"已生成许可证资产文件: {json_path} (共 {len(COMPONENTS_DATA)} 个组件, {len(LICENSES_CATALOG)} 个协议正文)")

def main() -> int:
    parser = argparse.ArgumentParser(description="校验或生成开源组件与协议正文资产")
    parser.add_argument("--json", type=Path, default=LICENSES_JSON, help="licenses.json 路径")
    parser.add_argument("--toml", type=Path, default=TOML_FILE, help="libs.versions.toml 路径")
    parser.add_argument("--generate", action="store_true", help="根据预置元数据生成 licenses.json")
    args = parser.parse_args()

    if args.generate:
        generate_licenses_asset(args.json)

    toml_libs = parse_toml_libraries(args.toml)
    errors = check_licenses_json(args.json, toml_libs)

    if errors:
        print(f"❌ 发现 {len(errors)} 项许可证元数据错误:", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    print(f"✅ 许可证资产校验通过: {args.json}")
    return 0

if __name__ == "__main__":
    sys.exit(main())
